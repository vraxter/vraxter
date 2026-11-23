// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package main

import (
	"context"
	"crypto/md5"
	"crypto/tls"
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
	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/client"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/env"
	"github.com/vraxter/vraxter/internal/features"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/server"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/internal/telemetry"
	"github.com/vraxter/vraxter/internal/tui"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
)

var (
	appConfig          config.Config
	appStore           *db.Store
	appCrypto          *security.CryptoService
	appEngine          *core.Engine
	appProviderManager *services.ProviderManager
	appModelManager    *services.ModelManager
	appSkillService    *services.SkillService
	appStateManager    *env.StateManager
	appPipeline        *env.ExecutionPipeline
	agentFlag          string
	sessionFlag        string
	modelFlag          string
	airgapFlag              bool
	enableHostExecFlag      bool
	requireSignedSkillsFlag bool
	newFlag                 bool
	verboseFlag        bool
	addrFlag           string
	listenTCPFlag      string
	tlsCAFlag          string
	tlsCertFlag        string
	tlsKeyFlag         string
	daemonKey          string

	bootstrapOnce sync.Once
)

func bootstrap() {
	bootstrapOnce.Do(func() {
		appConfig = config.Load()
		if airgapFlag {
			appConfig.IsolationMode = "airgap"
			appConfig.IsAirgapLocked = true
			appConfig.PrivacyPolicy = "strict_local"
		}
		if enableHostExecFlag {
			appConfig.HostExec.Enabled = true
		}
		if requireSignedSkillsFlag {
			appConfig.RequireSignedSkills = true
		}
		if strings.TrimSpace(listenTCPFlag) != "" {
			ep := strings.TrimSpace(listenTCPFlag)
			if !strings.HasPrefix(ep, "tcp://") && !strings.HasPrefix(ep, "unix://") {
				ep = "tcp://" + ep
			}
			appConfig.ListenAddr = ep
		} else if strings.TrimSpace(addrFlag) != "" {
			appConfig.ListenAddr = strings.TrimSpace(addrFlag)
		}

		// Configure process-wide gRPC client endpoint and optional mTLS 1.3 credentials
		clientTLS := client.ClientTLSOptions{
			CAPath:   firstNonEmpty(tlsCAFlag, appConfig.TLS.CAPath),
			CertPath: firstNonEmpty(tlsCertFlag, appConfig.TLS.ClientCert),
			KeyPath:  firstNonEmpty(tlsKeyFlag, appConfig.TLS.ClientKey),
		}
		client.ConfigureDefaultTransport(appConfig.DefaultLocalEndpoint(), clientTLS)

		// Enforce zero-egress network perimeter on all HTTP dials
		utils.InstallAirgapPerimeter(appConfig.IsolationMode, appConfig.WhitelistedIPs)
		_, _ = telemetry.Configure(telemetry.Config{
			IsolationMode: appConfig.IsolationMode,
		})
		
		// Setup logging: Redirect all background service logs to a file with restricted permissions (0600)
		logFile, err := os.OpenFile(filepath.Join(appConfig.AppDir, "vraxter.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err == nil {
			log.SetOutput(logFile)
			log.Println("--- Vraxter Engine Started ---")
		}

		daemonKey, err = security.GetOrCreateDaemonKey(appConfig.AppDir)
		if err != nil {
			fmt.Printf("Fatal: Error securing Daemon API Key: %v\n", err)
			os.Exit(1)
		}

		appStore, err = db.NewStoreWithOptions(appConfig.DBPath, db.StoreOptions{
			EncryptAtRest: appConfig.EncryptDBAtRest,
			MasterKeyPath: appConfig.DBMasterKeyPath,
		})
		if err != nil {
			fmt.Printf("Fatal: Error connecting to database: %v\n", err)
			os.Exit(1)
		}

		server.SetAPIKeyRepo(db.NewAPIKeyRepository(appStore))

		appCrypto, err = security.NewCryptoService(appConfig.KeyPath)
		if err != nil {
			fmt.Printf("Fatal: Error generating or securing AES Keys: %v\n", err)
			os.Exit(1)
		}

		registry := skills.NewRegistry()
		runner := skills.NewRunner()
		runner.SetNetworkPolicy(appConfig.IsolationMode, appConfig.WhitelistedIPs)
		runner.SetTrustedSignersDir(appConfig.TrustedSignersDir)
		runner.SetRequireSignedSkills(appConfig.RequireSignedSkills)

		skillRepo := db.NewSkillRepository(appStore)
		appSkillService = services.NewSkillService(skillRepo, appConfig.SkillsDir)
		appSkillService.SetTrustedSignersDir(appConfig.TrustedSignersDir)
		appSkillService.SetRequireSignedSkills(appConfig.RequireSignedSkills)
		if err := appSkillService.LoadAllIntoRegistry(registry); err != nil {
			fmt.Printf("Warning: Startup Skill synchronization failed: %v\n", err)
		}

		tm := services.NewToolchainManager(appConfig.SDKDir, appConfig.IsAirgap())
		coder := services.NewCoderService(skillRepo, tm, appConfig.SkillsDir, runner)
		spatialSvc := services.NewSpatialService(appConfig.EnableGoogleHome, appConfig.SpatialConfigPath)

		appEngine, err = core.NewEngine(appStore, appCrypto, registry, runner, coder, spatialSvc, verboseFlag, appConfig.AppDir, appConfig.RequireSkillApproval, appConfig.IsolationMode, appConfig.WhitelistedIPs)
		if err != nil {
			fmt.Printf("Fatal: Core Engine initialization failed: %v\n", err)
			os.Exit(1)
		}
		appEngine.SetHostExecGuard(security.NewHostExecGuard(security.HostExecOptions{
			Enabled:        appConfig.HostExec.Enabled,
			CommandsFile:   appConfig.HostExec.CommandsFile,
			DefaultTimeout: time.Duration(appConfig.HostExec.DefaultTimeoutSec) * time.Second,
			IsolationMode:  appConfig.IsolationMode,
			WhitelistedIPs: appConfig.WhitelistedIPs,
		}))
		if auditLedger, aErr := audit.NewLedger(appStore, appConfig.AppDir, appConfig.AuditJSONLPath); aErr == nil {
			appEngine.SetAuditLedger(auditLedger)
			audit.SetDefaultLedger(auditLedger)
		}

		providerRepo := db.NewProviderRepository(appStore, appCrypto)
		modelRepo := db.NewModelRepository(appStore, appCrypto)

		appProviderManager = services.NewProviderManager(providerRepo, appConfig.PrivacyPolicy, appConfig.WhitelistedIPs, appConfig.IsolationMode)
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
	// Commands are added via init() in their respective files

	rootCmd.Flags().StringVarP(&agentFlag, "agent", "a", "", "Delegate execution directly to a Specialist sub-agent ID")
	rootCmd.Flags().StringVarP(&sessionFlag, "session", "s", "", "Specify a topic or conversation ID to isolate context")
	rootCmd.Flags().BoolVarP(&newFlag, "new", "n", false, "Start a completely fresh session (random UUID)")
	rootCmd.PersistentFlags().BoolVarP(&verboseFlag, "verbose", "v", false, "Enable detailed logging")
	rootCmd.PersistentFlags().StringVarP(&modelFlag, "model", "m", "", "Force use of a specific LLM model ID/alias")
	rootCmd.PersistentFlags().BoolVar(&airgapFlag, "airgap", false, "Enforce strict zero-egress airgap perimeter")
	rootCmd.PersistentFlags().BoolVar(&enableHostExecFlag, "enable-host-exec", false, "Enable guarded local host command execution ($) via host_commands.json")
	rootCmd.PersistentFlags().BoolVar(&requireSignedSkillsFlag, "require-signed-skills", false, "Block installation and execution of unsigned skills (require valid Ed25519 signature)")
	rootCmd.PersistentFlags().StringVar(&addrFlag, "addr", "", "Target gRPC daemon endpoint (default: unix://<AppDir>/vraxter.sock on Unix, tcp://127.0.0.1:50051 on Windows)")
	rootCmd.PersistentFlags().StringVar(&listenTCPFlag, "listen-tcp", "", "Bind gRPC server to a TCP address with mandatory mTLS 1.3 (e.g. 0.0.0.0:50051)")
	rootCmd.PersistentFlags().StringVar(&tlsCAFlag, "tls-ca", "", "Path to mTLS 1.3 Root CA certificate PEM")
	rootCmd.PersistentFlags().StringVar(&tlsCertFlag, "tls-cert", "", "Path to mTLS 1.3 certificate PEM")
	rootCmd.PersistentFlags().StringVar(&tlsKeyFlag, "tls-key", "", "Path to mTLS 1.3 private key PEM")

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
			app.IsolationMode = appConfig.IsolationMode
			app.PrivacyPolicy = appConfig.PrivacyPolicy
			app.IsAirgapLocked = appConfig.IsAirgapLocked
			app.WhitelistedIPs = append([]string(nil), appConfig.WhitelistedIPs...)
			p := tea.NewProgram(app)
			if _, err := p.Run(); err != nil {
				fmt.Printf("❌ Failed to start UI: %v\n", err)
			}
			return
		}

		query := strings.Join(args, " ")

		// 2. Strict Remote Mode (gRPC Daemon)
		fmt.Printf("🔍 Connecting to Vraxter Daemon...")
		ep := client.DefaultEndpoint()
		gClient, err := client.NewGRPCClient(ep, daemonKey)
		if err != nil {
			fmt.Println(" [SPAWNING BACKGROUND DAEMON]")
			spawnEphemeralDaemon()
			time.Sleep(100 * time.Millisecond)
			gClient, err = client.NewGRPCClient(ep, daemonKey) // Connect to newly spawned daemon
			if err != nil {
				fmt.Printf("❌ Failed to connect to Vraxter Daemon after spawn: %v\n", err)
				return
			}
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
		case llm.EventTypeSkillApprovalRequest:
			if lastWasToken {
				fmt.Println()
			}
			fmt.Printf("\n🛡️  HITL Approval Required [%s]: %s\n", event.SkillID, event.Content)
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

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}

// resolveDaemonListener configures the gRPC listener for the daemon:
//   - Local UDS ("unix://...") on Unix/POSIX (with stale socket cleanup and 0600 permissions)
//   - Loopback TCP ("tcp://127.0.0.1:50051") on Windows by default
//   - Mandatory mTLS 1.3 whenever --listen-tcp, a non-loopback TCP address, or TLS flags are active
func resolveDaemonListener(cfg *config.Config) (net.Listener, grpc.ServerOption, string, error) {
	endpoint := cfg.DefaultLocalEndpoint()
	network, address, isNonLoopbackTCP, err := server.ParseEndpoint(endpoint)
	if err != nil {
		return nil, nil, "", err
	}

	forceMTLS := strings.TrimSpace(listenTCPFlag) != "" || cfg.TLS.Enabled ||
		strings.TrimSpace(tlsCAFlag) != "" || strings.TrimSpace(tlsCertFlag) != "" || strings.TrimSpace(tlsKeyFlag) != ""

	var tlsCfg *tls.Config
	if network == "tcp" && (isNonLoopbackTCP || forceMTLS) {
		caPath := firstNonEmpty(tlsCAFlag, cfg.TLS.CAPath)
		certPath := firstNonEmpty(tlsCertFlag, cfg.TLS.CertPath)
		keyPath := firstNonEmpty(tlsKeyFlag, cfg.TLS.KeyPath)

		// Auto-generate local self-signed ECDSA P-256 PKI if no explicit cert paths were supplied
		// and AutoGenPKI was not disabled.
		if caPath == "" && certPath == "" && keyPath == "" {
			pkiPaths, pkiErr := security.EnsureLocalPKI(cfg.AppDir, []string{address})
			if pkiErr != nil {
				return nil, nil, "", fmt.Errorf("failed to provision local mTLS 1.3 PKI: %w", pkiErr)
			}
			caPath = pkiPaths.CAPath
			certPath = pkiPaths.ServerCertPath
			keyPath = pkiPaths.ServerKeyPath

			// Also wire the matching local client credentials for intra-process connections.
			client.ConfigureDefaultTransport(endpoint, client.ClientTLSOptions{
				CAPath:   pkiPaths.CAPath,
				CertPath: pkiPaths.ClientCertPath,
				KeyPath:  pkiPaths.ClientKeyPath,
			})
		}

		tlsCfg, err = security.LoadServerTLSConfig(caPath, certPath, keyPath)
		if err != nil {
			return nil, nil, "", err
		}
	}

	lis, credOpt, err := server.ListenEndpoint(endpoint, tlsCfg, forceMTLS)
	if err != nil {
		return nil, nil, "", err
	}
	return lis, credOpt, endpoint, nil
}

// spawnEphemeralDaemon secretly boots the gRPC server within the current process if one doesn't exist
func spawnEphemeralDaemon() {
	bootstrap()

	lis, credOpt, _, err := resolveDaemonListener(&appConfig)
	if err != nil {
		fmt.Printf("Fatal: failed to listen on daemon endpoint: %v\n", err)
		os.Exit(1)
	}

	shutdownCh := make(chan struct{}, 1)
	srv := server.BuildServerWithShutdown(appEngine, appProviderManager, appModelManager, appSkillService, &appConfig, daemonKey, shutdownCh, credOpt)
	ready := make(chan bool)
	go func() {
		ready <- true
		if err := srv.Serve(lis); err != nil {
			fmt.Printf("Fatal: ephemeral gRPC server error: %v\n", err)
		}
	}()
	go func() {
		<-shutdownCh
		srv.GracefulStop()
		_ = lis.Close()
		if appStore != nil {
			_ = appStore.Close()
		}
	}()
	<-ready
}

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Starts the Vraxter API/gRPC background server",
	Run: func(cmd *cobra.Command, args []string) {
		bootstrap() // Server MUST have the full engine loaded

		lis, credOpt, endpoint, err := resolveDaemonListener(&appConfig)
		if err != nil {
			fmt.Printf("Fatal: failed to initialize gRPC listener: %v\n", err)
			os.Exit(1)
		}
		defer lis.Close()

		shutdownCh := make(chan struct{}, 1)
		grpcServer := server.BuildServerWithShutdown(appEngine, appProviderManager, appModelManager, appSkillService, &appConfig, daemonKey, shutdownCh, credOpt)

		go func() {
			if err := grpcServer.Serve(lis); err != nil {
				fmt.Printf("Fatal: gRPC server error: %v\n", err)
			}
		}()

		fmt.Printf("🚀 Vraxter Engine (gRPC) is Online on %s\n", endpoint)

		// Start Connect RPC Server with hardened DDIL HTTP timeouts
		mux := http.NewServeMux()
		server.RegisterEnvService(mux, appStateManager, appPipeline)

		httpServer := &http.Server{
			Addr:              "127.0.0.1:8080",
			Handler:           server.HTTPAuthMiddleware(daemonKey, mux),
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      60 * time.Second,
			IdleTimeout:       120 * time.Second,
		}

		fmt.Println("🌍 Connect RPC API listening on 127.0.0.1:8080 (HTTP)")
		go func() {
			if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Printf("Fatal: HTTP server error: %v\n", err)
			}
		}()

		// Graceful Shutdown Trapping (OS signal OR authenticated SYSTEM_SHUTDOWN RPC)
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		select {
		case <-stop:
		case <-shutdownCh:
		}

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
		gClient, err := client.NewGRPCClient(client.DefaultEndpoint(), daemonKey)
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
	ep := client.DefaultEndpoint()
	gClient, err := client.NewGRPCClient(ep, daemonKey)
	if err == nil {
		return gClient, nil
	}

	fmt.Printf("🔍 Spawning Background Daemon... ")
	spawnEphemeralDaemon()
	time.Sleep(100 * time.Millisecond) // Warm-up
	gClient, err = client.NewGRPCClient(ep, daemonKey)
	if err != nil {
		return nil, fmt.Errorf("failed to connect even after spawn: %w", err)
	}
	fmt.Println("[READY]")
	return gClient, nil
}
