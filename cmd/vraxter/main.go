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
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"log"
	"net"
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

	clientEnvOnce  sync.Once
	localStoreOnce sync.Once
	bootstrapOnce  sync.Once
)

// initClientEnv loads configuration, applies CLI transport/isolation flags, and installs
// the network perimeter WITHOUT opening SQLite, generating master keys, or booting Wazero.
func initClientEnv() {
	clientEnvOnce.Do(func() {
		appConfig = config.LoadReadOnly()
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
			if strings.TrimSpace(os.Getenv("VRAXTER_ALLOW_RUNTIME_CODEGEN")) == "" {
				appConfig.AllowRuntimeCodegen = false
			}
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
	})
}

// ensureDaemonKey loads (or creates on first run) the local daemon API key for authenticated gRPC calls.
func ensureDaemonKey() string {
	initClientEnv()
	if daemonKey != "" {
		return daemonKey
	}
	appConfig.EnsureDirs()
	var err error
	daemonKey, err = security.GetOrCreateDaemonKey(appConfig.AppDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Fatal: Error securing Daemon API Key: %v\n", err)
		os.Exit(1)
	}
	return daemonKey
}

// ensureLocalStore opens the local SQLite store (acquiring vraxter.db.lock) and AES crypto service
// ONLY for explicit offline local-DB CLI commands when no daemon is active.
func ensureLocalStore() {
	initClientEnv()
	localStoreOnce.Do(func() {
		appConfig.EnsureDirs()
		if appStore == nil {
			var err error
			appStore, err = db.NewStoreWithOptions(appConfig.DBPath, db.StoreOptions{
				EncryptAtRest: appConfig.EncryptDBAtRest,
				MasterKeyPath: appConfig.DBMasterKeyPath,
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "Fatal: Error connecting to database: %v\n", err)
				os.Exit(1)
			}
		}
		if appCrypto == nil {
			var err error
			appCrypto, err = security.NewCryptoService(appConfig.KeyPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Fatal: Error generating or securing AES Keys: %v\n", err)
				os.Exit(1)
			}
		}
	})
}

// shouldSkipClientEnvInit reports whether cmd is a pure read-only/meta command that should
// run with zero filesystem side effects.
func shouldSkipClientEnvInit(cmd *cobra.Command) bool {
	if cmd == nil {
		return false
	}
	switch cmd.Name() {
	case "version", "help", "__complete", "__completeNoDesc":
		return true
	}
	return false
}

func bootstrap() {
	initClientEnv()
	bootstrapOnce.Do(func() {
		appConfig.EnsureDirs()
		_, _ = telemetry.Configure(telemetry.Config{
			IsolationMode: appConfig.IsolationMode,
		})

		// Setup logging: Redirect all background service logs to a file with restricted permissions (0600)
		logFile, err := os.OpenFile(filepath.Join(appConfig.AppDir, "vraxter.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err == nil {
			log.SetOutput(logFile)
			log.Println("--- Vraxter Engine Started ---")
		}

		ensureDaemonKey()
		ensureLocalStore()

		server.SetAPIKeyRepo(db.NewAPIKeyRepository(appStore))

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

		appEngine, err = core.NewEngine(appStore, appCrypto, registry, runner, coder, verboseFlag, appConfig.AppDir, appConfig.RequireSkillApproval, appConfig.IsolationMode, appConfig.WhitelistedIPs)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Fatal: Core Engine initialization failed: %v\n", err)
			_ = appStore.Close()
			os.Exit(1)
		}
		appEngine.SetAllowRuntimeCodegen(appConfig.AllowRuntimeCodegen)
		appEngine.SetInferenceDefaults(appConfig.Inference.Temperature, appConfig.Inference.Seed, appConfig.Inference.MaxTokens)
		appEngine.SetHostExecGuard(security.NewHostExecGuard(security.HostExecOptions{
			Enabled:        appConfig.HostExec.Enabled,
			CommandsFile:   appConfig.HostExec.CommandsFile,
			DefaultTimeout: time.Duration(appConfig.HostExec.DefaultTimeoutSec) * time.Second,
			IsolationMode:  appConfig.IsolationMode,
			WhitelistedIPs: appConfig.WhitelistedIPs,
		}))

		// Fail-closed: the daemon must never run without an intact forensic audit ledger.
		auditLedger, aErr := audit.NewLedger(appStore, appConfig.AppDir, appConfig.AuditJSONLPath)
		if aErr != nil {
			fmt.Fprintf(os.Stderr, "Fatal: Forensic audit ledger initialization failed: %v\n", aErr)
			_ = appStore.Close()
			os.Exit(1)
		}
		appEngine.SetAuditLedger(auditLedger)
		audit.SetDefaultLedger(auditLedger)

		providerRepo := db.NewProviderRepository(appStore, appCrypto)
		modelRepo := db.NewModelRepository(appStore, appCrypto)

		appProviderManager = services.NewProviderManager(providerRepo, appConfig.PrivacyPolicy, appConfig.WhitelistedIPs, appConfig.IsolationMode)
		appModelManager = services.NewModelManager(modelRepo, providerRepo)

		// Automigrations
		if err := db.RenameProviderType(appStore, "gemini", "google"); err != nil {
			log.Printf("Warning: gemini->google migration failed: %v", err)
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
		sum := sha256.Sum256([]byte(uuid.New().String()))
		return fmt.Sprintf("new-%d-%x", time.Now().Unix(), sum[:8])
	}
	if sessionFlag != "" {
		return sessionFlag
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "cli-default"
	}
	sum := sha256.Sum256([]byte(cwd))
	return fmt.Sprintf("workspace-%x", sum[:8])
}

var rootCmd = &cobra.Command{
	Use:   "vraxter",
	Short: "Vraxter - Sovereign Autonomous Execution Runtime",
	Long: `Vraxter is a zero-egress, pure-Go Sovereign Autonomous Execution Runtime for
critical infrastructure, defense, and enterprise enclaves.

Core security guarantees:
  • Capability-bounded WASM/WASI sandboxing (CPU Fuel, QuotaFS, memory caps)
  • Cryptographic Ed25519 skill supply-chain signing and verification
  • Immutable hash-chained & Ed25519-signed forensic audit ledger
  • AES-256-GCM encryption at rest for SQLite state and snapshots
  • Local UDS (0600) and mTLS 1.3 authenticated gRPC control plane`,
	Args: cobra.ArbitraryArgs,
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if shouldSkipClientEnvInit(cmd) {
			return
		}
		initClientEnv()
	},
	Run: func(cmd *cobra.Command, args []string) {
		ctx := context.Background()
		sessionID := resolveSession()

		if len(args) == 0 {
			// 1. REPL Mode over gRPC (connect to running daemon without opening SQLite, or spawn ephemeral embedded daemon)
			gClient, err := connectOrSpawn(ctx)
			if err != nil {
				fmt.Printf("❌ Fatal: %v\n", err)
				return
			}

			userName := "YOU"
			if appStore != nil {
				userRepo := db.NewUserRepository(appStore)
				if user, err := userRepo.GetDefaultUser(ctx); err == nil && user != nil && user.Name != "" {
					userName = user.Name
				}
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
		key := ensureDaemonKey()
		gClient, err := client.NewGRPCClient(ep, key)
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

		if err := writeDaemonPIDFile(appConfig.AppDir, os.Getpid()); err != nil {
			log.Printf("Warning: failed writing daemon PID file: %v", err)
		}
		defer removeDaemonPIDFile(appConfig.AppDir)

		shutdownCh := make(chan struct{}, 1)
		grpcServer := server.BuildServerWithShutdown(appEngine, appProviderManager, appModelManager, appSkillService, &appConfig, daemonKey, shutdownCh, credOpt)

		go func() {
			if err := grpcServer.Serve(lis); err != nil {
				fmt.Printf("Fatal: gRPC server error: %v\n", err)
			}
		}()

		fmt.Printf("🚀 Vraxter Sovereign Runtime (gRPC) is Online on %s [isolation=%s]\n", endpoint, appConfig.IsolationMode)

		// Graceful Shutdown Trapping (OS signal OR authenticated SYSTEM_SHUTDOWN RPC)
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
		select {
		case <-stop:
		case <-shutdownCh:
		}

		fmt.Println("\n🛑 Shutting down Vraxter Daemon gracefully...")

		grpcServer.GracefulStop()
		_ = lis.Close()
		appStore.Close()
		fmt.Println("✅ Vraxter stopped successfully.")
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)
}

// connectOrSpawn attempts to connect to an active daemon without opening SQLite locally,
// spawning an embedded daemon only if no external daemon is reachable.
func connectOrSpawn(ctx context.Context) (*client.GRPCClient, error) {
	ep := client.DefaultEndpoint()
	key := ensureDaemonKey()
	gClient, err := client.NewGRPCClient(ep, key)
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
