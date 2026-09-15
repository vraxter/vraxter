// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"github.com/spf13/cobra"
	"github.com/patagonicrune/vraxter/internal/client"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
	"log"
)

var providersCmd = &cobra.Command{
	Use:   "providers [id] [action]",
	Short: "Manage AI service providers (Google, OpenAI, Ollama, etc.)",
	Run: func(cmd *cobra.Command, args []string) {
		// Handle the workstation-grade positional syntax: providers <id> discover
		if len(args) == 2 && args[1] == "discover" {
			executeDiscovery(args[0])
			return
		}

		if len(args) == 0 {
			cmd.Help()
			return
		}

		// Handle setups
		if args[0] == "setup" {
			executeSetup()
			return
		}

		// Handle positional model syntax: providers <provider_id> model <model_id> [--add | --info]
		if len(args) >= 3 && args[1] == "model" {
			executeModelAction(args[0], args[2], cmd)
			return
		}

		fmt.Printf("❌ Unknown command or syntax: %v\n", args)
		fmt.Println("Did you mean: vraxter providers <id> discover")
	},
}

func executeDiscovery(providerID string) {
	ctx := context.Background()

	// 1. Try gRPC (Bypass for internal TUI sessions to preserve rich metadata)
	if os.Getenv("VRAXTER_INTERNAL_SESSION") != "true" {
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
		if err == nil {
			defer gClient.Close()
			models, err := gClient.DiscoverModels(ctx, providerID)
			if err == nil {
				// Convert legacy string slice to rich metadata for rendering
				rich := make([]interfaces.ModelMetadata, len(models))
				for i, id := range models {
					rich[i] = interfaces.ModelMetadata{
						ID:           id,
						DisplayName:  id,
						Capabilities: []string{"text"},
					}
				}
				renderDiscovery(providerID, rich)
				return
			}
		}
	}

	// 2. Local Fallback
	if os.Getenv("VRAXTER_INTERNAL_SESSION") != "true" {
		fmt.Printf("💡 [LOCAL FALLBACK] Daemon offline. Discovering via local engine.\n")
	}
	manager := getProviderManager()
	models, err := manager.DiscoverModels(ctx, providerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "❌ Discovery failed: %v\n", err)
		os.Exit(1)
	}

	renderDiscovery(providerID, models)
}

var providersListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all configured providers",
	Run: func(cmd *cobra.Command, args []string) {
		manager := getProviderManager()
		list, err := manager.ListProviders()
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to list providers: %v\n", err)
			os.Exit(1)
		}

		fmt.Println("\n◈ VRAXTER PROVIDERS")
		fmt.Println("--------------------------------------------------------------------------------")
		fmt.Printf("%-10s %-20s %-12s %-30s\n", "ID", "NAME", "TYPE", "STATUS")
		fmt.Println("--------------------------------------------------------------------------------")
		for _, p := range list {
			status := "🟢 Active"
			if !p.IsActive {
				status = "⚪ Disabled"
			}
			fmt.Printf("%-10s %-20s %-12s %-30s\n", p.ID, p.Name, p.Type, status)
		}
		fmt.Println("--------------------------------------------------------------------------------")
	},
}

var providersSupportedCmd = &cobra.Command{
	Use:   "supported",
	Short: "List all provider types supported by this Vraxter build",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		// 1. Try gRPC
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
		if err == nil {
			defer gClient.Close()
			list, err := gClient.GetSupportedProviders(ctx)
			if err == nil {
				renderSupported(list)
				return
			}
		}

		// 2. Local Fallback
		if os.Getenv("VRAXTER_INTERNAL_SESSION") != "true" {
			fmt.Printf("💡 [LOCAL FALLBACK] Daemon offline. Accessing local registry.\n")
		}
		manager := getProviderManager()
		list := manager.GetSupportedProviders()
		renderSupported(list)
	},
}

func renderSupported(types []string) {
	fmt.Println("\n◈ SUPPORTED PROVIDER TYPES")
	fmt.Println("--------------------------------------------------------------------------------")
	for _, t := range types {
		fmt.Printf("  • %s\n", t)
	}
	fmt.Println("--------------------------------------------------------------------------------")
	fmt.Println("Use 'vraxter providers add --type <type>' to configure one.")
}

var providersAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a new AI provider",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		name := args[0]
		pType, _ := cmd.Flags().GetString("type")
		key, _ := cmd.Flags().GetString("key")
		url, _ := cmd.Flags().GetString("url")

		if pType == "" {
			fmt.Printf("❌ Missing mandatory flag: --type is required\n")
			fmt.Printf("Example: vraxter providers add %s --type google --key YOUR_TOKEN\n", name)
			os.Exit(1)
		}

		ctx := context.Background()

		// 1. Try gRPC
		gClient, err := client.NewManagementClient("127.0.0.1:50051", daemonKey)
		if err == nil {
			defer gClient.Close()
			id, err := gClient.ConfigureProvider(ctx, name, pType, key, url)
			if err == nil {
				fmt.Printf("✨ Provider '%s' added successfully via Daemon! ID: %s\n", name, id)
				return
			}
		}

		// 2. Local Fallback
		if os.Getenv("VRAXTER_INTERNAL_SESSION") != "true" {
			fmt.Printf("💡 [LOCAL FALLBACK] Daemon offline. Configuring locally.\n")
		}
		manager := getProviderManager()
		id, err := manager.AddProvider(ctx, name, pType, key, url)
		if err != nil {
			log.Fatalf("❌ Failed to add provider: %v", err)
		}

		fmt.Printf("✨ Provider '%s' added successfully! ID: %s\n", name, id)
	},
}

// Keep the legacy command for internal routing if needed, but remove from main init
var providersDiscoverCmd = &cobra.Command{
	Use:    "discover <id>",
	Short:  "Discover available models (Internal use, please use providers <id> discover)",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		executeDiscovery(args[0])
	},
}

func executeSetup() {
	var name, pType, key, url string

	fmt.Println("\n◈ VRAXTER PROVIDER SETUP")
	fmt.Println("--------------------------------------------------------------------------------")
	
	fmt.Print("1. Enter Provider Name (e.g. MyGoogle): ")
	fmt.Scanln(&name)
	
	fmt.Print("2. Enter Type (google, openai, anthropic, ollama): ")
	fmt.Scanln(&pType)
	
	fmt.Print("3. Enter API Key: ")
	fmt.Scanln(&key)
	
	fmt.Print("4. Enter Base URL (Optional, press Enter to skip): ")
	fmt.Scanln(&url)

	if name == "" || pType == "" {
		fmt.Println("❌ Error: Name and Type are mandatory.")
		return
	}

	ctx := context.Background()
	manager := getProviderManager()
	id, err := manager.AddProvider(ctx, name, pType, key, url)
	if err != nil {
		fmt.Printf("❌ Failed to add provider: %v\n", err)
		return
	}

	fmt.Printf("\n✨ Provider '%s' added successfully! ID: %s\n", name, id)
}

func executeModelAction(providerID, modelID string, cmd *cobra.Command) {
	ctx := context.Background()
	manager := getProviderManager()
	mManager := getModelManager()

	showInfo, _ := cmd.Flags().GetBool("info")
	doAdd, _ := cmd.Flags().GetBool("add")

	if showInfo {
		details, err := manager.GetModelDetails(ctx, providerID, modelID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to get model details: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("\n◈ MODEL DETAILS: %s\n", modelID)
		fmt.Println("--------------------------------------------------------------------------------")
		for k, v := range details {
			fmt.Printf("%-20s: %v\n", k, v)
		}
		fmt.Println("--------------------------------------------------------------------------------")
	}

	if doAdd {
		id, err := mManager.AddModel(ctx, providerID, modelID, 5, "")
		if err != nil {
			fmt.Fprintf(os.Stderr, "❌ Failed to add model: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Model '%s' added successfully with ID: %s\n", modelID, id)
	}

	if !showInfo && !doAdd {
		fmt.Println("💡 Specify --info to see details or --add to register the model.")
	}
}

func renderDiscovery(pID string, models []interfaces.ModelMetadata) {
	groups := make(map[string][]interfaces.ModelMetadata)
	for _, m := range models {
		group := "LLM (Text)"
		isEmbedding := false
		isAudio := false
		isImage := false
		for _, c := range m.Capabilities {
			if c == "embedding" { isEmbedding = true }
			if c == "audio" { isAudio = true }
			if c == "image" { isImage = true }
		}

		if isEmbedding {
			group = "Embedding"
		} else if isImage {
			group = "Image"
		} else if isAudio {
			group = "Audio"
		}
		groups[group] = append(groups[group], m)
	}

	fmt.Printf("\n◈ REMOTE MODELS DISCOVERED FOR: %s\n", pID)
	fmt.Println("------------------------------------------------------------------------------------------------")
	fmt.Printf("%-45s %-12s %-20s\n", "MODEL ID", "CONTEXT", "CAPABILITIES")
	fmt.Println("------------------------------------------------------------------------------------------------")

	order := []string{"LLM (Text)", "Embedding", "Audio", "Image"}
	for _, gTitle := range order {
		ms := groups[gTitle]
		if len(ms) == 0 {
			continue
		}

		fmt.Printf("\n[%s]\n", gTitle)
		for _, m := range ms {
			ctx := "N/A"
			if m.ContextWindow > 0 {
				ctx = formatContextWindow(m.ContextWindow)
			}
			caps := strings.Join(m.Capabilities, ", ")
			fmt.Printf("  • %-41s %-12s %-20s\n", m.ID, ctx, caps)
		}
	}

	fmt.Println("------------------------------------------------------------------------------------------------")
	fmt.Printf("Use 'vraxter providers %s model <id> --add' to activate one of these models.\n\n", pID)
}

func formatContextWindow(n int) string {
	if n >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(n)/1000000.0)
	}
	if n >= 1000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%d", n)
}

func init() {
	providersAddCmd.Flags().String("type", "", "Provider type (openai, google, anthropic, ollama)")
	providersAddCmd.Flags().String("key", "", "API Key for the service")
	providersAddCmd.Flags().String("url", "", "Custom Base URL (optional)")
	providersAddCmd.MarkFlagRequired("type")

	// Global flags for positional model command
	providersCmd.PersistentFlags().Bool("add", false, "Add the model to Vraxter automatically")
	providersCmd.PersistentFlags().Bool("info", false, "Display detailed technical info for the model")

	providersCmd.AddCommand(providersListCmd)
	providersCmd.AddCommand(providersSupportedCmd)
	providersCmd.AddCommand(providersAddCmd)
	providersCmd.AddCommand(providersDiscoverCmd)
	rootCmd.AddCommand(providersCmd)
}

// Utility to get the manager
func getProviderManager() *services.ProviderManager {
	bootstrap() // Ensure appStore and appCrypto are ready
	repo := db.NewProviderRepository(appStore, appCrypto)
	return services.NewProviderManager(repo, appConfig.PrivacyPolicy, appConfig.WhitelistedIPs)
}
