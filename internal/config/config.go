// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vraxter/vraxter/internal/utils"
)

// HostExecConfig controls local guarded host command execution ($).
// Enabled is always false by default and can ONLY be toggled locally via --enable-host-exec
// or local settings.json (never mutable over gRPC).
type HostExecConfig struct {
	Enabled           bool   `json:"enabled"`
	CommandsFile      string `json:"commands_file,omitempty"`
	DefaultTimeoutSec int    `json:"default_timeout_sec,omitempty"`
}

// TLSConfig holds mTLS 1.3 certificate and key paths for network TCP transport.
type TLSConfig struct {
	Enabled    bool   `json:"enabled,omitempty"`
	AutoGenPKI bool   `json:"auto_gen_pki,omitempty"`
	CAPath     string `json:"ca_path,omitempty"`
	CertPath   string `json:"cert_path,omitempty"`
	KeyPath    string `json:"key_path,omitempty"`
	ClientCert string `json:"client_cert,omitempty"`
	ClientKey  string `json:"client_key,omitempty"`
}

// InferenceConfig controls deterministic LLM inference defaults.
type InferenceConfig struct {
	Temperature *float64 `json:"temperature,omitempty"`
	Seed        *int64   `json:"seed,omitempty"`
	MaxTokens   int      `json:"max_tokens,omitempty"`
}

// Config holds global daemon configuration for the Vraxter Sovereign Runtime.
type Config struct {
	AppDir               string
	DBPath               string
	KeyPath              string
	DBMasterKeyPath      string
	SocketPath           string
	ListenAddr           string
	SkillsDir            string
	SDKDir               string
	TrustedSignersDir    string
	AuditJSONLPath       string
	PrivacyPolicy        string
	IsolationMode        string   // "airgap", "local_lan", "hybrid"
	IsAirgapLocked       bool     // True when airgap is enforced by immutable env/flag
	HubURL               string   // Optional private enterprise skill registry URL (empty by default)
	RequireSkillApproval bool
	RequireSignedSkills  bool
	AllowRuntimeCodegen  bool
	EncryptDBAtRest      bool
	WhitelistedIPs       []string
	HostExec             HostExecConfig
	TLS                  TLSConfig
	Inference            InferenceConfig
}

type Settings struct {
	PrivacyPolicy        string          `json:"privacy_policy"`
	IsolationMode        string          `json:"isolation_mode,omitempty"`
	HubURL               string          `json:"hub_url,omitempty"`
	RequireSkillApproval bool            `json:"require_skill_approval"`
	RequireSignedSkills  bool            `json:"require_signed_skills,omitempty"`
	AllowRuntimeCodegen  *bool           `json:"allow_runtime_codegen,omitempty"`
	EncryptDBAtRest      bool            `json:"encrypt_db_at_rest,omitempty"`
	AuditJSONLPath       string          `json:"audit_jsonl_path,omitempty"`
	WhitelistedIPs       []string        `json:"whitelisted_ips"`
	HostExec             HostExecConfig  `json:"host_exec"`
	ListenAddr           string          `json:"listen_addr,omitempty"`
	TLS                  TLSConfig       `json:"tls,omitempty"`
	Inference            InferenceConfig `json:"inference,omitempty"`
}

// DefaultHubURL can be overridden at build time using -ldflags "-X github.com/vraxter/vraxter/internal/config.DefaultHubURL=..."
// Empty by default: Sovereign deployments inject signed offline skills locally or point to an internal enterprise registry.
var DefaultHubURL = ""

// Load loads configuration from environment or system defaults and ensures runtime directories exist.
func Load() Config {
	cfg := LoadReadOnly()
	cfg.EnsureDirs()
	return cfg
}

// EnsureDirs creates the local Vraxter data directories with restricted 0700 permissions.
func (c *Config) EnsureDirs() {
	_ = os.MkdirAll(c.AppDir, 0700)
	_ = os.MkdirAll(c.SkillsDir, 0700)
	_ = os.MkdirAll(c.SDKDir, 0700)
	_ = os.MkdirAll(c.TrustedSignersDir, 0700)
}

// LoadReadOnly resolves configuration from settings.json and environment variables
// without creating any files or directories on disk.
func LoadReadOnly() Config {
	// 1. Priority: Explicit environment variable
	appDir := os.Getenv("VRAXTER_APP_DIR")

	if appDir == "" {
		// 2. Standard: Use OS specific configuration directory
		configDir, err := os.UserConfigDir()
		if err != nil {
			home, _ := os.UserHomeDir()
			appDir = filepath.Join(home, ".vraxter")
		} else {
			appDir = filepath.Join(configDir, "vraxter")
		}
	}

	skillsDir := filepath.Join(appDir, "skills")
	sdkDir := filepath.Join(appDir, "sdk")
	trustedSignersDir := filepath.Join(appDir, "trusted_signers")

	// 3. Load from settings.json if exists
	settingsPath := filepath.Join(appDir, "settings.json")
	var privacy = "strict_local"
	var isolationMode = "airgap" // Default secure
	var isAirgapLocked = false
	var hub = DefaultHubURL
	var requireSkillApproval = false
	var requireSignedSkills = false
	var allowRuntimeCodegenPtr *bool
	var encryptDBAtRest = false
	var auditJSONLPath string
	var whitelistedIPs []string
	var listenAddr string
	var tlsCfg TLSConfig
	var inferenceCfg InferenceConfig
	hostExec := HostExecConfig{
		Enabled:           false,
		CommandsFile:      filepath.Join(appDir, "host_commands.json"),
		DefaultTimeoutSec: 15,
	}

	if data, err := os.ReadFile(settingsPath); err == nil {
		var s Settings
		if json.Unmarshal(data, &s) == nil {
			if s.PrivacyPolicy != "" {
				privacy = s.PrivacyPolicy
			}
			if s.IsolationMode != "" {
				isolationMode = s.IsolationMode
			} else if s.PrivacyPolicy == "strict_local" {
				isolationMode = "airgap"
			} else if s.PrivacyPolicy == "custom" || s.PrivacyPolicy == "ask" {
				isolationMode = "hybrid"
			}
			if s.HubURL != "" {
				hub = s.HubURL
			}
			requireSkillApproval = s.RequireSkillApproval
			requireSignedSkills = s.RequireSignedSkills
			allowRuntimeCodegenPtr = s.AllowRuntimeCodegen
			encryptDBAtRest = s.EncryptDBAtRest
			if strings.TrimSpace(s.AuditJSONLPath) != "" {
				auditJSONLPath = strings.TrimSpace(s.AuditJSONLPath)
			}
			if len(s.WhitelistedIPs) > 0 {
				whitelistedIPs = s.WhitelistedIPs
			}
			hostExec.Enabled = s.HostExec.Enabled
			if s.HostExec.CommandsFile != "" {
				hostExec.CommandsFile = s.HostExec.CommandsFile
			}
			if s.HostExec.DefaultTimeoutSec > 0 {
				hostExec.DefaultTimeoutSec = s.HostExec.DefaultTimeoutSec
			}
			if strings.TrimSpace(s.ListenAddr) != "" {
				listenAddr = strings.TrimSpace(s.ListenAddr)
			}
			tlsCfg = s.TLS
			inferenceCfg = s.Inference
		}
	}

	// Override with env var if explicitly set
	if envPrivacy := os.Getenv("VRAXTER_PRIVACY_POLICY"); envPrivacy != "" {
		privacy = envPrivacy
		if envPrivacy == "strict_local" {
			isolationMode = "airgap"
		}
	}
	if envIso := os.Getenv("VRAXTER_ISOLATION_MODE"); envIso != "" {
		isolationMode = envIso
		if envIso == "airgap" {
			isAirgapLocked = true
		}
	}
	if envAirgap := os.Getenv("VRAXTER_AIRGAP"); envAirgap == "true" || envAirgap == "1" {
		isolationMode = "airgap"
		isAirgapLocked = true
	}
	if isolationMode == "airgap" {
		privacy = "strict_local"
	}
	if envHub := os.Getenv("VRAXTER_HUB_URL"); envHub != "" {
		hub = envHub
	}
	if envReqApp := os.Getenv("VRAXTER_REQUIRE_SKILL_APPROVAL"); envReqApp == "true" {
		requireSkillApproval = true
	}
	if envReqSig := os.Getenv("VRAXTER_REQUIRE_SIGNED_SKILLS"); envReqSig == "true" || envReqSig == "1" {
		requireSignedSkills = true
	}
	allowRuntimeCodegen := !requireSignedSkills
	if allowRuntimeCodegenPtr != nil {
		allowRuntimeCodegen = *allowRuntimeCodegenPtr
	}
	if envCodegen := strings.ToLower(strings.TrimSpace(os.Getenv("VRAXTER_ALLOW_RUNTIME_CODEGEN"))); envCodegen != "" {
		allowRuntimeCodegen = envCodegen == "true" || envCodegen == "1"
	}
	if envEncDB := os.Getenv("VRAXTER_ENCRYPT_DB_AT_REST"); envEncDB == "true" || envEncDB == "1" {
		encryptDBAtRest = true
	}
	if envAuditJSONL := strings.TrimSpace(os.Getenv("VRAXTER_AUDIT_JSONL_PATH")); envAuditJSONL != "" {
		auditJSONLPath = envAuditJSONL
	}
	if envAddr := strings.TrimSpace(os.Getenv("VRAXTER_GRPC_ADDR")); envAddr != "" {
		listenAddr = envAddr
	}

	socketPath := filepath.Join(appDir, "vraxter.sock")
	if envSock := strings.TrimSpace(os.Getenv("VRAXTER_SOCKET_PATH")); envSock != "" {
		socketPath = envSock
	}

	return Config{
		AppDir:               appDir,
		DBPath:               filepath.Join(appDir, "vraxter.db"),
		KeyPath:              filepath.Join(appDir, ".masterkey"),
		DBMasterKeyPath:      filepath.Join(appDir, "db_master.key"),
		SocketPath:           socketPath,
		ListenAddr:           listenAddr,
		SkillsDir:            skillsDir,
		SDKDir:               sdkDir,
		TrustedSignersDir:    trustedSignersDir,
		AuditJSONLPath:       auditJSONLPath,
		PrivacyPolicy:        privacy,
		IsolationMode:        isolationMode,
		IsAirgapLocked:       isAirgapLocked,
		HubURL:               hub,
		RequireSkillApproval: requireSkillApproval,
		RequireSignedSkills:  requireSignedSkills,
		AllowRuntimeCodegen:  allowRuntimeCodegen,
		EncryptDBAtRest:      encryptDBAtRest,
		WhitelistedIPs:       whitelistedIPs,
		HostExec:             hostExec,
		TLS:                  tlsCfg,
		Inference:            inferenceCfg,
	}
}

// DefaultLocalEndpoint returns the default gRPC endpoint for local IPC:
//   - Unix/POSIX: "unix://<AppDir>/vraxter.sock"
//   - Windows: "tcp://127.0.0.1:50051" (unless ListenAddr or explicit unix:// is configured)
func (c *Config) DefaultLocalEndpoint() string {
	if addr := strings.TrimSpace(c.ListenAddr); addr != "" {
		return addr
	}
	if runtime.GOOS == "windows" {
		return "tcp://127.0.0.1:50051"
	}
	sock := strings.TrimSpace(c.SocketPath)
	if sock == "" {
		sock = filepath.Join(c.AppDir, "vraxter.sock")
	}
	return "unix://" + sock
}

// SaveSettings writes minimal settings back to the filesystem atomically.
func (c *Config) SaveSettings() error {
	c.EnsureDirs()
	allowCodegen := c.AllowRuntimeCodegen
	s := Settings{
		PrivacyPolicy:        c.PrivacyPolicy,
		IsolationMode:        c.IsolationMode,
		HubURL:               c.HubURL,
		RequireSkillApproval: c.RequireSkillApproval,
		RequireSignedSkills:  c.RequireSignedSkills,
		AllowRuntimeCodegen:  &allowCodegen,
		EncryptDBAtRest:      c.EncryptDBAtRest,
		AuditJSONLPath:       c.AuditJSONLPath,
		WhitelistedIPs:       c.WhitelistedIPs,
		HostExec:             c.HostExec,
		ListenAddr:           c.ListenAddr,
		TLS:                  c.TLS,
		Inference:            c.Inference,
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return utils.AtomicWriteFile(filepath.Join(c.AppDir, "settings.json"), data, 0600)
}

// IsAirgap returns true if Vraxter is strictly running in zero-egress airgap mode
func (c *Config) IsAirgap() bool {
	return c.IsolationMode == "airgap" || c.PrivacyPolicy == "strict_local"
}

// IsLocalLAN returns true if local private subnets are permitted
func (c *Config) IsLocalLAN() bool {
	return c.IsolationMode == "local_lan"
}

// IsHybrid returns true if internet communication is allowed
func (c *Config) IsHybrid() bool {
	return !c.IsAirgap() && !c.IsLocalLAN()
}
