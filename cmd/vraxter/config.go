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
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/vraxter/vraxter/internal/client"
	"github.com/vraxter/vraxter/internal/config"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage enclave security posture and runtime configuration",
	Long: `Inspect and update Vraxter Sovereign Runtime configuration.

Local security boundary settings (require_signed_skills, encrypt_db_at_rest,
audit_jsonl_path, allow_runtime_codegen, host_exec.enabled) are stored in
local settings.json (0600) and are never mutable over remote gRPC.`,
}

var configListCmd = &cobra.Command{
	Use:   "list",
	Short: "Display active enclave security and runtime configuration",
	Run: func(cmd *cobra.Command, args []string) {
		cfg := config.LoadReadOnly()
		source := "local settings.json (daemon offline)"

		isolationMode := cfg.IsolationMode
		isAirgapLocked := cfg.IsAirgapLocked
		privacyPolicy := cfg.PrivacyPolicy
		hubURL := cfg.HubURL
		requireSkillApproval := cfg.RequireSkillApproval
		whitelistedIPs := cfg.WhitelistedIPs

		// Only query live daemon via gRPC if a daemon key already exists (zero side effects on fresh AppDir)
		daemonKeyPath := filepath.Join(cfg.AppDir, ".daemon_key")
		if rawKey, err := os.ReadFile(daemonKeyPath); err == nil {
			key := strings.TrimSpace(string(rawKey))
			if key != "" {
				if gClient, dialErr := client.NewManagementClient(client.DefaultEndpoint(), key); dialErr == nil {
					defer gClient.Close()
					if res, rpcErr := gClient.GetConfig(context.Background()); rpcErr == nil {
						source = "live daemon + local enclave policy"
						isolationMode = res.IsolationMode
						isAirgapLocked = res.IsAirgapLocked
						privacyPolicy = res.PrivacyPolicy
						hubURL = res.HubUrl
						requireSkillApproval = res.RequireSkillApproval
						whitelistedIPs = res.WhitelistedIps
					}
				}
			}
		}

		lockStr := "dynamic"
		if isAirgapLocked {
			lockStr = "locked (immutable)"
		}

		auditJSONLDisplay := cfg.AuditJSONLPath
		if auditJSONLDisplay == "" {
			auditJSONLDisplay = "(disabled — SQLite audit ledger only)"
		}
		hubDisplay := hubURL
		if hubDisplay == "" {
			hubDisplay = "(disabled — offline local injection)"
		}

		fmt.Printf("\n⚙️ VRAXTER ENCLAVE CONFIGURATION [%s]\n", source)
		fmt.Println("--------------------------------------------------------------")
		fmt.Printf("isolation_mode         : %s (%s)\n", isolationMode, lockStr)
		fmt.Printf("privacy_policy         : %s\n", privacyPolicy)
		fmt.Printf("require_skill_approval : %v\n", requireSkillApproval)
		fmt.Printf("require_signed_skills  : %v\n", cfg.RequireSignedSkills)
		fmt.Printf("allow_runtime_codegen  : %v\n", cfg.AllowRuntimeCodegen)
		fmt.Printf("encrypt_db_at_rest     : %v\n", cfg.EncryptDBAtRest)
		fmt.Printf("audit_jsonl_path       : %s\n", auditJSONLDisplay)
		fmt.Printf("host_exec.enabled      : %v\n", cfg.HostExec.Enabled)
		fmt.Printf("hub_url                : %s\n", hubDisplay)
		if len(whitelistedIPs) > 0 {
			fmt.Printf("whitelisted_ips        : %v\n", whitelistedIPs)
		}
		fmt.Println("--------------------------------------------------------------")
	},
}

func parseBoolConfigValue(raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "true", "1", "yes", "on":
		return true, nil
	case "false", "0", "no", "off":
		return false, nil
	default:
		return false, fmt.Errorf("invalid boolean value %q (expected true/false)", raw)
	}
}

var configSetCmd = &cobra.Command{
	Use:   "set <key> <value>",
	Short: "Set an enclave configuration key in local settings.json and/or live daemon",
	Long: `Set a configuration value.

Supported keys:
  isolation_mode         airgap | local_lan | hybrid
  privacy_policy         strict_local | local_lan | ask | custom
  require_skill_approval true | false
  require_signed_skills  true | false  (local enclave policy; restart daemon to apply)
  allow_runtime_codegen  true | false  (local enclave policy; restart daemon to apply)
  encrypt_db_at_rest     true | false  (local enclave policy; restart daemon to apply)
  audit_jsonl_path       <path> | ""   (local enclave policy; restart daemon to apply)
  host_exec.enabled      true | false  (local-only; never mutable over gRPC)
  hub_url                <url> | ""    (optional private enterprise registry)`,
	Args: cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		key := strings.TrimSpace(args[0])
		val := strings.TrimSpace(args[1])

		// Local-only enclave security settings are persisted directly to settings.json (0600)
		// and are intentionally never accepted over remote gRPC.
		switch key {
		case "require_signed_skills", "allow_runtime_codegen", "encrypt_db_at_rest", "audit_jsonl_path", "host_exec.enabled":
			cfg := config.Load()
			switch key {
			case "require_signed_skills":
				b, err := parseBoolConfigValue(val)
				if err != nil {
					fmt.Printf("❌ %v\n", err)
					os.Exit(1)
				}
				cfg.RequireSignedSkills = b
				if b {
					cfg.AllowRuntimeCodegen = false
				}
			case "allow_runtime_codegen":
				b, err := parseBoolConfigValue(val)
				if err != nil {
					fmt.Printf("❌ %v\n", err)
					os.Exit(1)
				}
				cfg.AllowRuntimeCodegen = b
			case "encrypt_db_at_rest":
				b, err := parseBoolConfigValue(val)
				if err != nil {
					fmt.Printf("❌ %v\n", err)
					os.Exit(1)
				}
				cfg.EncryptDBAtRest = b
			case "audit_jsonl_path":
				cfg.AuditJSONLPath = val
			case "host_exec.enabled":
				b, err := parseBoolConfigValue(val)
				if err != nil {
					fmt.Printf("❌ %v\n", err)
					os.Exit(1)
				}
				cfg.HostExec.Enabled = b
			}
			if err := cfg.SaveSettings(); err != nil {
				fmt.Printf("❌ Failed saving settings.json: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("✅ Updated local enclave policy %s = %s (restart daemon if running to apply).\n", key, val)
			return
		}

		// Runtime-mutable settings: apply via gRPC when daemon is running, otherwise persist locally.
		var pp, hurl, iso *string
		var rsa *bool
		switch key {
		case "isolation_mode":
			if val != "airgap" && val != "local_lan" && val != "hybrid" {
				fmt.Printf("❌ Invalid isolation_mode %q (expected: airgap, local_lan, hybrid)\n", val)
				os.Exit(1)
			}
			iso = &val
		case "privacy_policy":
			pp = &val
		case "hub_url":
			hurl = &val
		case "require_skill_approval":
			b, err := parseBoolConfigValue(val)
			if err != nil {
				fmt.Printf("❌ %v\n", err)
				os.Exit(1)
			}
			rsa = &b
		default:
			fmt.Printf("❌ Unknown configuration key '%s'.\nSupported: isolation_mode, privacy_policy, require_skill_approval, require_signed_skills, allow_runtime_codegen, encrypt_db_at_rest, audit_jsonl_path, host_exec.enabled, hub_url\n", key)
			os.Exit(1)
		}

		cfg := config.Load()
		if cfg.IsAirgapLocked && (key == "isolation_mode" || key == "privacy_policy") && val != "airgap" && val != "strict_local" {
			fmt.Println("❌ Security policy locked: airgap mode is enforced via environment/flag and cannot be relaxed.")
			os.Exit(1)
		}

		daemonKeyPath := filepath.Join(cfg.AppDir, ".daemon_key")
		if rawKey, err := os.ReadFile(daemonKeyPath); err == nil {
			if k := strings.TrimSpace(string(rawKey)); k != "" {
				if gClient, dialErr := client.NewManagementClient(client.DefaultEndpoint(), k); dialErr == nil {
					defer gClient.Close()
					if _, rpcErr := gClient.UpdateConfig(context.Background(), pp, hurl, rsa, nil, iso); rpcErr == nil {
						fmt.Printf("✅ Updated %s = %s (applied to running daemon and settings.json).\n", key, val)
						return
					}
				}
			}
		}

		if iso != nil {
			cfg.IsolationMode = *iso
			if *iso == "airgap" {
				cfg.PrivacyPolicy = "strict_local"
			}
		}
		if pp != nil {
			cfg.PrivacyPolicy = *pp
			if *pp == "strict_local" {
				cfg.IsolationMode = "airgap"
			}
		}
		if hurl != nil {
			cfg.HubURL = *hurl
		}
		if rsa != nil {
			cfg.RequireSkillApproval = *rsa
		}
		if err := cfg.SaveSettings(); err != nil {
			fmt.Printf("❌ Error persisting settings.json: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Updated %s = %s in local settings.json.\n", key, val)
	},
}

func init() {
	configCmd.AddCommand(configListCmd)
	configCmd.AddCommand(configSetCmd)
	rootCmd.AddCommand(configCmd)
}
