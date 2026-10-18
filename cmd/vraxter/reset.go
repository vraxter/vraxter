// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vraxter/vraxter/internal/client"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/db"
)

var (
	resetFactoryFlag  bool
	resetHardFlag     bool
	resetForceFlag    bool
	resetNoBackupFlag bool
	resetSessionsFlag bool
)

var resetCmd = &cobra.Command{
	Use:   "reset",
	Short: "Reset Vraxter state or restore to factory defaults",
	Long: `Reset Vraxter state, purge sessions, or restore everything to clean factory defaults.

Examples:
  vraxter reset --factory          # Interactive full factory reset (backs up first)
  vraxter reset --factory --force  # Non-interactive factory reset
  vraxter reset --sessions         # Clear all conversation sessions`,
	Run: func(cmd *cobra.Command, args []string) {
		isFactory := resetFactoryFlag || resetHardFlag

		if !isFactory && !resetSessionsFlag {
			fmt.Println("⚠️  No reset mode specified.")
			fmt.Println()
			fmt.Println("Usage:")
			fmt.Println("  vraxter reset --factory         Full factory reset (wipes DB, keys, and configurations)")
			fmt.Println("  vraxter reset --factory --force Skip confirmation prompt")
			fmt.Println("  vraxter reset --sessions        Clear all saved conversation sessions")
			return
		}

		if resetSessionsFlag {
			if appStore == nil {
				bootstrap()
			}
			chatRepo := db.NewChatRepository(appStore)
			convs, err := chatRepo.ListConversations(1000)
			if err != nil {
				fmt.Printf("❌ Failed to query sessions: %v\n", err)
				return
			}
			for _, c := range convs {
				_ = chatRepo.DeleteConversation(c.ID)
			}
			fmt.Printf("✅ Cleared %d conversation session(s).\n", len(convs))
			if !isFactory {
				return
			}
		}

		if isFactory {
			if !resetForceFlag {
				fmt.Println("⚠️  WARNING: Factory Reset will erase all providers, models, specialists,")
				fmt.Println("             API keys, and settings. A backup will be preserved automatically.")
				fmt.Print("Are you sure you want to proceed? [y/N]: ")
				reader := bufio.NewReader(os.Stdin)
				input, _ := reader.ReadString('\n')
				input = strings.TrimSpace(strings.ToLower(input))
				if input != "y" && input != "yes" {
					fmt.Println("Operation cancelled.")
					return
				}
			}

			// 1. Attempt to stop running daemon gracefully
			fmt.Print("🛑 Shutting down any running background daemon... ")
			gClient, err := client.NewGRPCClient("127.0.0.1:50051", daemonKey)
			if err == nil {
				_, _ = gClient.ExecuteStream(context.Background(), "SYSTEM_SHUTDOWN", "internal-mgmt", "", "")
				gClient.Close()
				time.Sleep(300 * time.Millisecond)
				fmt.Println("[DONE]")
			} else {
				fmt.Println("[OFFLINE]")
			}

			// 2. Close active store connection in this process if open
			if appStore != nil {
				appStore.Close()
				appStore = nil
			}

			// 3. Perform Factory Reset
			createBackup := !resetNoBackupFlag
			backupPath, err := config.PerformFactoryReset(createBackup)
			if err != nil {
				fmt.Printf("❌ Factory reset failed: %v\n", err)
				os.Exit(1)
			}

			fmt.Println()
			fmt.Println("✨ VRAXTER FACTORY RESET COMPLETE ✨")
			fmt.Println("--------------------------------------------------")
			if backupPath != "" {
				fmt.Printf("📦 Backup preserved at: %s\n", backupPath)
			}
			fmt.Println("🌱 Clean workspace initialized.")
			fmt.Println("🚀 Run 'vraxter' to start with a fresh workstation.")
			fmt.Println("--------------------------------------------------")
		}
	},
}

func init() {
	resetCmd.Flags().BoolVarP(&resetFactoryFlag, "factory", "f", false, "Restore Vraxter completely to factory defaults")
	resetCmd.Flags().BoolVar(&resetHardFlag, "hard", false, "Alias for --factory")
	resetCmd.Flags().BoolVarP(&resetForceFlag, "force", "y", false, "Bypass interactive confirmation")
	resetCmd.Flags().BoolVar(&resetNoBackupFlag, "no-backup", false, "Do not create a backup before wiping data")
	resetCmd.Flags().BoolVarP(&resetSessionsFlag, "sessions", "s", false, "Clear all saved conversation sessions")
	rootCmd.AddCommand(resetCmd)
}

