package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/patagonicrune/vraxter/internal/client"
	"github.com/patagonicrune/vraxter/internal/config"
	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/security"
	"github.com/patagonicrune/vraxter/internal/server"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/spf13/cobra"
)

var (
	appConfig config.Config
	appStore  *db.Store
	appCrypto *security.CryptoService
	appEngine *core.Engine
	agentFlag   string
	verboseFlag bool

	bootstrapOnce sync.Once
)

func bootstrap() {
	bootstrapOnce.Do(func() {
		appConfig = config.Load()

		var err error
		appStore, err = db.NewStore(appConfig.DBPath)
		if err != nil {
			fmt.Printf("Fatal: Error connecting to database: %v\n", err)
			os.Exit(1)
		}

		appCrypto, err = security.NewCryptoService(appConfig.KeyPath)
		if err != nil {
			fmt.Printf("Fatal: Error generating or securing AES Keys: %v\n", err)
			os.Exit(1)
		}

		registry := skills.NewRegistry()
		runner := skills.NewRunner()

		skillRepo := db.NewSkillRepository(appStore)
		skillService := services.NewSkillService(skillRepo, appConfig.SkillsDir)
		if err := skillService.LoadAllIntoRegistry(registry); err != nil {
			fmt.Printf("Warning: Startup Skill synchronization failed: %v\n", err)
		}

		tm := services.NewToolchainManager(appConfig.SDKDir)
		coder := services.NewCoderService(skillRepo, tm, appConfig.SkillsDir)

		appEngine, err = core.NewEngine(appStore, appCrypto, registry, runner, coder, verboseFlag)
		if err != nil {
			fmt.Printf("Fatal: Core Engine initialization failed: %v\n", err)
			os.Exit(1)
		}
	})
}


func main() {
	// Handle redundant "vraxter" word often caused by 'go run'
	if len(os.Args) > 1 && os.Args[1] == "vraxter" {
		os.Args = append(os.Args[:1], os.Args[2:]...)
	}

	// appStore must be closed gracefully if initialized
	defer func() {
		if appStore != nil {
			appStore.Close()
		}
	}()

	rootCmd.AddCommand(serverCmd)
	rootCmd.AddCommand(addCmd)
	rootCmd.AddCommand(modelsCmd)
	rootCmd.AddCommand(skillsCmd)

	rootCmd.Flags().StringVarP(&agentFlag, "agent", "a", "", "Delegate execution directly to a Specialist sub-agent ID")
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "Enable detailed logging")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "vraxter",
	Short: "Vraxter - Local-first AI agent engine",
	Args:  cobra.ArbitraryArgs,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		// Only bootstrap if we are NOT using a sub-action that manages its own bootstrap
		// or if we are the root command running standalone.
		// For now, let's just bootstrap always for simplicity in the CLI
		bootstrap() 
	},
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()

		if len(args) == 0 {
			// 2. Fallback: Standalone Mode (Full Bootstrap)
			fmt.Println(" [OFFLINE]")
			fmt.Printf("🧠 Starting Local Motor (REPL Mode)...")
			bootstrap()
			fmt.Println(" [READY]")
			fmt.Println("\nWelcome to Vraxter. Press Ctrl+C to exit.")
			
			scanner := bufio.NewScanner(os.Stdin)
			for {
				fmt.Print("\n👤 You: ")
				if !scanner.Scan() {
					break
				}
				query := scanner.Text()
				if strings.TrimSpace(query) == "" {
					continue
				}

				stream, err := appEngine.ProcessRawIntent(ctx, query, agentFlag)
				if err != nil {
					fmt.Printf("❌ Engine Error: %v\n", err)
					continue
				}
				processStream(stream)
			}
			return
		}

		query := strings.Join(args, " ")

		// 1. Try Remote Mode (gRPC Daemon)
		fmt.Printf("🔍 Connecting to Vraxter Daemon...")
		gClient, err := client.NewGRPCClient(":50051")
		if err == nil {
			fmt.Println(" [CONNECTED]")
			stream, err := gClient.ExecuteStream(ctx, query)
			if err != nil {
				fmt.Printf("❌ gRPC stream error: %v\n", err)
				return
			}
			processStream(stream)
			return
		}

		// 2. Fallback: Standalone Mode (Full Bootstrap)
		fmt.Println(" [OFFLINE]")
		fmt.Printf("🧠 Starting Local Motor (Standalone)...")
		bootstrap()
		fmt.Println(" [READY]")

		stream, err := appEngine.ProcessRawIntent(ctx, query, agentFlag)
		if err != nil {
			fmt.Printf("❌ Engine Error: %v\n", err)
			return
		}
		processStream(stream)
	},
}

// processStream is a unified helper to handle both Local and gRPC events
func processStream(stream <-chan llm.StreamEvent) {
	fmt.Print("\n💬 Vraxter: ")
	var sentenceBuffer strings.Builder
	lastWasToken := false

	for event := range stream {
		switch event.Type {
		case llm.EventTypeToken:
			fmt.Print(event.Content)
			sentenceBuffer.WriteString(event.Content)
			lastWasToken = true

			// Voice/Punctuation Logic (Placeholder)
			if strings.ContainsAny(event.Content, ".!?") {
				sentenceBuffer.Reset()
			}
		case llm.EventTypeError:
			if lastWasToken { fmt.Println() }
			fmt.Printf("\n❌ Error: %v\n", event.Err)
			lastWasToken = false
		case llm.EventTypeSkillCall:
			if lastWasToken { fmt.Println() }
			fmt.Printf("\n⚙️  Running tool: [%s]...\n", event.Content)
			lastWasToken = false
		case llm.EventTypeDone:
			// Stream completed
		}
	}
	fmt.Println() // Final newline for system prompts/logs
}


var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Starts the Vraxter API/gRPC background server",
	Run: func(cmd *cobra.Command, args []string) {
		bootstrap() // Server MUST have the full engine loaded

		ready := make(chan bool)
		go func() {
			server.Start(appEngine, ready)
		}()

		<-ready
		fmt.Println("🚀 Vraxter Engine (gRPC) is Online.")
		select {} // Block main thread
	},
}

