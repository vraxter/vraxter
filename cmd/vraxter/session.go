// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage conversation sessions",
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List recent conversation sessions",
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewChatRepository(appStore)
		conversations, err := repo.ListConversations(20)
		if err != nil {
			fmt.Printf("❌ Failed to list sessions: %v\n", err)
			return
		}

		if len(conversations) == 0 {
			fmt.Println("No sessions found.")
			return
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "SESSION ID\tTITLE\tMESSAGES\tUPDATED")
		for _, c := range conversations {
			fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", c.ID, c.Title, c.MessageCount, c.UpdatedAt.Format("2006-01-02 15:04:05"))
		}
		w.Flush()
	},
}

var sessionClearCmd = &cobra.Command{
	Use:   "clear [session_id]",
	Short: "Clear a session history",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		repo := db.NewChatRepository(appStore)
		if err := repo.DeleteConversation(args[0]); err != nil {
			fmt.Printf("❌ Failed to clear session %s: %v\n", args[0], err)
			return
		}
		fmt.Printf("✅ Session %s cleared successfully.\n", args[0])
	},
}

func init() {
	sessionCmd.AddCommand(sessionListCmd)
	sessionCmd.AddCommand(sessionClearCmd)
	rootCmd.AddCommand(sessionCmd)
}
