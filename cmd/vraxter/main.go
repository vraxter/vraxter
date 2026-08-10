package main

import (
	"context"
	"crypto/md5"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/google/uuid"
	tea "charm.land/bubbletea/v2"
	"github.com/patagonicrune/vraxter/internal/client"
	"github.com/patagonicrune/vraxter/internal/config"
	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/env"
	"github.com/patagonicrune/vraxter/internal/features"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/security"
	"github.com/patagonicrune/vraxter/internal/server"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/internal/tui"
	"github.com/spf13/cobra"
)

var (
	appConfig   config.Config
	appStore    *db.Store
	appCrypto   *security.CryptoService
	appEngine   *core.Engine
	appProviderManager *services.ProviderManager
	appModelManager    *services.ModelManager
	appSkillService    *services.SkillService
	appStateManager    *env.StateManager
	appPipeline        *env.ExecutionPipeline
	agentFlag          string
	sessionFlag        string
	modelFlag          string
	newFlag            bool
	verboseFlag        bool
	daemonKey          string

	bootstrapOnce sync.Once
)

func bootstrap() {
	bootstrapOnce.Do(func() {
		appConfig = config.Load()
		
		// Setup logging: Redirect all background service logs to a file
		logFile, err := os.OpenFile(filepath.Join(appConfig.AppDir, "vraxter.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
		if err == nil {
			log.SetOutput(logFile)
			log.Println("--- Vraxter Engine Started ---")
		}

		daemonKey, err = security.GetOrCreateDaemonKey(appConfig.AppDir)
		if err != nil {
			fmt.Printf("Fatal: Error securing Daemon API Key: %v\n", err)
			os.Exit(1)
		}

		appStore, err = db.NewStore(appConfig.DBPath)
		if err != nil {
			fmt.Printf("Fatal: Error connecting to database: %v\n", err)
			os.Exit(1)
		}

		// appStore has been initialized above

		appCrypto, err = security.NewCryptoService(appConfig.KeyPath)
		if err != nil {
			fmt.Printf("Fatal: Error generating or securing AES Keys: %v\n", err)
			os.Exit(1)
		}

		registry := skills.NewRegistry()
		runner := skills.NewRunner()

		skillRepo := db.NewSkillRepository(appStore)
		appSkillService = services.NewSkillService(skillRepo, appConfig.SkillsDir)
		if err := appSkillService.LoadAllIntoRegistry(registry); err != nil {
			fmt.Printf("Warning: Startup Skill synchronization failed: %v\n", err)
		}

		tm := services.NewToolchainManager(appConfig.SDKDir)
		coder := services.NewCoderService(skillRepo, tm, appConfig.SkillsDir, runner)
		spatialSvc := services.NewSpatialService(appConfig.EnableGoogleHome, appConfig.SpatialConfigPath)

		appEngine, err = core.NewEngine(appStore, appCrypto, registry, runner, coder, spatialSvc, verboseFlag, appConfig.AppDir, appConfig.RequireSkillApproval)
		if err != nil {
			fmt.Printf("Fatal: Core Engine initialization failed: %v\n", err)
			os.Exit(1)
		}

		providerRepo := db.NewProviderRepository(appStore, appCrypto)
		modelRepo := db.NewModelRepository(appStore, appCrypto)

		appProviderManager = services.NewProviderManager(providerRepo, appConfig.PrivacyPolicy)
		appModelManager = services.NewModelManager(modelRepo, providerRepo)

		appStateManager, err = env.NewStateManager(appConfig.AppDir)
		if err != nil {
			fmt.Printf("Fatal: State Manager initialization failed: %v\n", err)
			os.Exit(1)
		}
		appPipeline = env.NewExecutionPipeline()

		// 3. Automigrations
		if err := db.RenameProviderType(appStore, "gemini", "google"); err != nil {
			log.Printf("Warning: gemini->google migration failed: %v", err)
		}

		// 4. Activate World Powers
		features.ActivateForWorld(appConfig.Implementation)
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
	rootCmd.AddCommand(daemonCmd)
	rootCmd.AddCommand(modelsCmd)
	rootCmd.AddCommand(skillsCmd)
	rootCmd.AddCommand(userCmd)

	rootCmd.Flags().StringVarP(&agentFlag, "agent", "a", "", "Delegate execution directly to a Specialist sub-agent ID")
	rootCmd.Flags().StringVarP(&sessionFlag, "session", "s", "", "Specify a topic or conversation ID to isolate context")
	rootCmd.Flags().BoolVarP(&newFlag, "new", "n", false, "Start a completely fresh session (random UUID)")
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "Enable detailed logging")
	rootCmd.PersistentFlags().StringVarP(&modelFlag, "model", "m", "", "Force use of a specific LLM model ID/alias")

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func getActiveOverride() string {
	if modelFlag != "" {
		return modelFlag
	}
	// Manual override from 'vraxter models activate'
	p := filepath.Join(config.Load().AppDir, "active_model")
	data, err := os.ReadFile(p)
	if err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

// resolveSession computes a conversation ID for the CLI.
// If explicitly provided via --session, it's used directly.
// Otherwise, it hashes the current working directory to group commands by project folder.
func resolveSession() string {
	if newFlag {
		return fmt.Sprintf("new-%d-%x", time.Now().Unix(), md5.Sum([]byte(uuid.New().String())))
	}
	if sessionFlag != "" {
		return sessionFlag
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "cli-default"
	}
	return fmt.Sprintf("workspace-%x", md5.Sum([]byte(cwd)))
}

var rootCmd = &cobra.Command{
	Use:   "vraxter",
	Short: "Vraxter - Local-first AI agent engine",
	Args:  cobra.ArbitraryArgs,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		bootstrap()
	},
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		sessionID := resolveSession()

		if len(args) == 0 {
			// 1. REPL Mode over gRPC
			gClient, err := connectOrSpawn(ctx)
			if err != nil {
				fmt.Printf("❌ Fatal: %v\n", err)
				return
			}

			userName := "YOU"
			userRepo := db.NewUserRepository(appStore)
			if user, err := userRepo.GetDefaultUser(ctx); err == nil && user != nil && user.Name != "" {
				userName = user.Name
			}

			defer gClient.Close()
			app := tui.NewModel(ctx, gClient, appStore, appCrypto, sessionID, "", userName)
			p := tea.NewProgram(app)
			if _, err := p.Run(); err != nil {
				fmt.Printf("❌ Failed to start UI: %v\n", err)
			}
			return
		}

		query := strings.Join(args, " ")

		// 2. Strict Remote Mode (gRPC Daemon)
		fmt.Printf("🔍 Connecting to Vraxter Daemon...")
		gClient, err := client.NewGRPCClient("127.0.0.1:50051", daemonKey)
		if err != nil {
			fmt.Println(" [SPAWNING BACKGROUND DAEMON]")
			spawnEphemeralDaemon()
			gClient, _ = client.NewGRPCClient("127.0.0.1:50051", daemonKey) // Connect to newly spawned daemon
		} else {
			fmt.Println(" [CONNECTED]")
		}
		defer gClient.Close()
		// Session isolation is now enforced in v1.1 via resolveSession()
		stream, err := gClient.ExecuteStream(ctx, query, sessionID, agentFlag, getActiveOverride())
		if err != nil {
			fmt.Printf("❌ gRPC stream error: %v\n", err)
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

	isThinking := false
	for event := range stream {
		switch event.Type {
		case llm.EventTypeToken:
			content := event.Content

			// Detect Chain-of-Thought (CoT)
			if !isThinking && strings.Contains(content, "<thought>") {
				isThinking = true
				parts := strings.SplitN(content, "<thought>", 2)
				fmt.Print(parts[0])
				fmt.Print("\n\033[2m> Thinking...\033[0m\n\033[3m") // Faint + Italic
				fmt.Print(parts[1])
				continue
			}
			if isThinking && strings.Contains(content, "</thought>") {
				isThinking = false
				parts := strings.SplitN(content, "</thought>", 2)
				fmt.Print(parts[0])
				fmt.Print("\033[0m\n") // Reset formatting
				fmt.Print(parts[1])
				continue
			}

			fmt.Print(content)
			sentenceBuffer.WriteString(content)
			lastWasToken = true

			// Voice/Punctuation Logic (Placeholder)
			if strings.ContainsAny(event.Content, ".!?") {
				sentenceBuffer.Reset()
			}
		case llm.EventTypeError:
			if lastWasToken {
				fmt.Println()
			}
			fmt.Printf("\n❌ Error: %v\n", event.Err)
			lastWasToken = false
		case llm.EventTypeSkillCall:
			if lastWasToken {
				fmt.Println()
			}
			fmt.Printf("\n⚙️  Running tool: [%s]...\n", event.Content)
			lastWasToken = false
		case llm.EventTypePlanProposal:
			if lastWasToken {
				fmt.Println()
			}
			fmt.Printf("\n📋 PROPOSED PLAN:\n%s\n", event.Content)
			lastWasToken = false
		case llm.EventTypeStatus:
			if lastWasToken {
				fmt.Println()
			}
			fmt.Printf("💡 %s\n", event.Content)
			lastWasToken = false
		case llm.EventTypeDone:
			// Stream completed
		}
	}
	fmt.Println() // Final newline for system prompts/logs
}

// spawnEphemeralDaemon secretly boots the gRPC server within the current process if one doesn't exist
func spawnEphemeralDaemon() {
	bootstrap()
	
	lis, err := net.Listen("tcp", "127.0.0.1:50051")
	if err != nil {
		fmt.Printf("Fatal: failed to listen on port: %v\n", err)
		os.Exit(1)
	}

	srv := server.BuildServer(appEngine, appProviderManager, appModelManager, appSkillService, &appConfig, daemonKey)
	ready := make(chan bool)
	go func() {
		ready <- true
		if err := srv.Serve(lis); err != nil {
			fmt.Printf("Fatal: ephemeral gRPC server error: %v\n", err)
		}
	}()
	<-ready
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Starts the Vraxter API/gRPC background server",
	Run: func(cmd *cobra.Command, args []string) {
		bootstrap() // Server MUST have the full engine loaded

		lis, err := net.Listen("tcp", "127.0.0.1:50051")
		if err != nil {
			fmt.Printf("Fatal: failed to listen on port: %v\n", err)
			os.Exit(1)
		}

		grpcServer := server.BuildServer(appEngine, appProviderManager, appModelManager, appSkillService, &appConfig, daemonKey)

		go func() {
			if err := grpcServer.Serve(lis); err != nil {
				fmt.Printf("Fatal: gRPC server error: %v\n", err)
			}
		}()

		fmt.Println("🚀 Vraxter Engine (gRPC) is Online on 127.0.0.1:50051")

		// Start Connect RPC Server
		mux := http.NewServeMux()
		server.RegisterEnvService(mux, appStateManager, appPipeline)

		httpServer := &http.Server{
			Addr:    "127.0.0.1:8080",
			Handler: server.HTTPAuthMiddleware(daemonKey, mux),
		}

		fmt.Println("🌍 Connect RPC API listening on 127.0.0.1:8080 (HTTP)")
		go func() {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Printf("Fatal: HTTP server error: %v\n", err)
			}
		}()

		// Graceful Shutdown Trapping
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		<-stop

		fmt.Println("\n🛑 Shutting down Vraxter Daemon gracefully...")

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := httpServer.Shutdown(ctx); err != nil {
			fmt.Printf("HTTP Server shutdown error: %v\n", err)
		}

		grpcServer.GracefulStop()
		appStore.Close()
		fmt.Println("✅ Vraxter stopped successfully.")
	},
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage the Vraxter gRPC background server",
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Gracefully shuts down the background daemon",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		gClient, err := client.NewGRPCClient("127.0.0.1:50051", daemonKey)
		if err != nil {
			fmt.Println("❌ Daemon is not running or unreachable.")
			return
		}
		defer gClient.Close()

		fmt.Printf("🛑 Sending shutdown signal... ")
		_, _ = gClient.ExecuteStream(ctx, "SYSTEM_SHUTDOWN", "internal-mgmt", "", "")
		fmt.Println("[SENT]")
	},
}

var daemonRestartCmd = &cobra.Command{
	Use:   "restart",
	Short: "Restart the Vraxter background daemon",
	Run: func(cmd *cobra.Command, args []string) {
		daemonStopCmd.Run(cmd, args)
		fmt.Println("♻️  Restarting daemon...")
		spawnEphemeralDaemon()
		fmt.Println("🚀 Daemon restarted.")
	},
}

func init() {
	daemonCmd.AddCommand(daemonStopCmd, daemonRestartCmd)
	rootCmd.AddCommand(serverCmd, daemonCmd)
}

// connectOrSpawn attempts to connect to a daemon, spawning one if invisible
func connectOrSpawn(ctx context.Context) (*client.GRPCClient, error) {
	gClient, err := client.NewGRPCClient("127.0.0.1:50051", daemonKey)
	if err == nil {
		return gClient, nil
	}

	fmt.Printf("🔍 Spawning Background Daemon... ")
	spawnEphemeralDaemon()
	time.Sleep(100 * time.Millisecond) // Warm-up
	gClient, err = client.NewGRPCClient("127.0.0.1:50051", daemonKey)
	if err != nil {
		return nil, fmt.Errorf("failed to connect even after spawn: %w", err)
	}
	fmt.Println("[READY]")
	return gClient, nil
}
