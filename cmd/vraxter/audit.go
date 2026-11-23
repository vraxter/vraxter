// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package main

import (
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
)

var (
	auditDBFlag      string
	auditJSONLFlag   string
	auditOutFlag     string
	auditSinceFlag   string
	auditTraceIDFlag string
)

var auditCmd = &cobra.Command{
	Use:   "audit",
	Short: "Verify or export the immutable hash-chained forensic audit ledger",
}

var auditVerifyCmd = &cobra.Command{
	Use:   "verify",
	Short: "Cryptographically verify the hash chain and Ed25519 signatures of the audit ledger",
	Run: func(cmd *cobra.Command, args []string) {
		count, err := runAuditVerify(auditDBFlag, auditJSONLFlag)
		if err != nil {
			fmt.Printf("❌ AUDIT VERIFICATION FAILED: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Audit ledger integrity verified (%d sequential entries, hash chain and Ed25519 signatures intact).\n", count)
	},
}

var auditExportCmd = &cobra.Command{
	Use:   "export",
	Short: "Export the forensic audit ledger to a verifiable JSONL file",
	Run: func(cmd *cobra.Command, args []string) {
		count, err := runAuditExport(auditDBFlag, auditOutFlag, auditSinceFlag, auditTraceIDFlag)
		if err != nil {
			fmt.Printf("❌ Audit export failed: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("✅ Exported %d audit entries to %s\n", count, auditOutFlag)
	},
}

func runAuditVerify(dbPathFlag, jsonlPathFlag string) (int, error) {
	cfg := appConfig
	if cfg.AppDir == "" {
		cfg = config.Load()
	}

	if strings.TrimSpace(jsonlPathFlag) != "" {
		var pubKey ed25519.PublicKey
		pubPath := filepath.Join(cfg.AppDir, "audit_identity.pub")
		if loadedPub, _, err := security.LoadEd25519PublicKey(pubPath); err == nil {
			pubKey = loadedPub
		}
		return audit.VerifyJSONLFile(strings.TrimSpace(jsonlPathFlag), pubKey)
	}

	targetDB := strings.TrimSpace(dbPathFlag)
	var store *db.Store
	var closeStore bool

	if targetDB != "" && targetDB != cfg.DBPath {
		s, err := db.NewStore(targetDB)
		if err != nil {
			return 0, fmt.Errorf("failed opening audit database %s: %w", targetDB, err)
		}
		store = s
		closeStore = true
	} else if appStore != nil {
		store = appStore
	} else {
		if targetDB == "" {
			targetDB = cfg.DBPath
		}
		s, err := db.NewStore(targetDB)
		if err != nil {
			return 0, fmt.Errorf("failed opening audit database %s: %w", targetDB, err)
		}
		store = s
		closeStore = true
	}
	if closeStore {
		defer store.Close()
	}

	identityDir := cfg.AppDir
	if strings.TrimSpace(dbPathFlag) != "" {
		candDir := filepath.Dir(strings.TrimSpace(dbPathFlag))
		if _, err := os.Stat(filepath.Join(candDir, "audit_identity.key")); err == nil {
			identityDir = candDir
		}
	}

	ledger, err := audit.NewLedger(store, identityDir, "")
	if err != nil {
		return 0, err
	}
	return ledger.VerifyChain()
}

func runAuditExport(dbPathFlag, outPathFlag, sinceFlag, traceIDFlag string) (int, error) {
	outPath := strings.TrimSpace(outPathFlag)
	if outPath == "" {
		return 0, fmt.Errorf("--out <file.jsonl> is required")
	}

	var filter audit.ExportFilter
	if strings.TrimSpace(sinceFlag) != "" {
		ts, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(sinceFlag))
		if err != nil {
			ts, err = time.Parse(time.RFC3339, strings.TrimSpace(sinceFlag))
			if err != nil {
				return 0, fmt.Errorf("invalid --since timestamp %q (expected RFC3339): %w", sinceFlag, err)
			}
		}
		filter.Since = ts
	}
	if strings.TrimSpace(traceIDFlag) != "" {
		filter.TraceID = strings.TrimSpace(traceIDFlag)
	}

	cfg := appConfig
	if cfg.AppDir == "" {
		cfg = config.Load()
	}

	targetDB := strings.TrimSpace(dbPathFlag)
	var store *db.Store
	var closeStore bool

	if targetDB != "" && targetDB != cfg.DBPath {
		s, err := db.NewStore(targetDB)
		if err != nil {
			return 0, fmt.Errorf("failed opening audit database %s: %w", targetDB, err)
		}
		store = s
		closeStore = true
	} else if appStore != nil {
		store = appStore
	} else {
		if targetDB == "" {
			targetDB = cfg.DBPath
		}
		s, err := db.NewStore(targetDB)
		if err != nil {
			return 0, fmt.Errorf("failed opening audit database %s: %w", targetDB, err)
		}
		store = s
		closeStore = true
	}
	if closeStore {
		defer store.Close()
	}

	identityDir := cfg.AppDir
	if strings.TrimSpace(dbPathFlag) != "" {
		candDir := filepath.Dir(strings.TrimSpace(dbPathFlag))
		if _, err := os.Stat(filepath.Join(candDir, "audit_identity.key")); err == nil {
			identityDir = candDir
		}
	}

	ledger, err := audit.NewLedger(store, identityDir, "")
	if err != nil {
		return 0, err
	}
	return ledger.ExportJSONL(outPath, filter)
}

func init() {
	auditVerifyCmd.Flags().StringVar(&auditDBFlag, "db", "", "Path to SQLite database file (defaults to active Vraxter DB)")
	auditVerifyCmd.Flags().StringVar(&auditJSONLFlag, "jsonl", "", "Path to exported or live JSONL audit file to verify offline")

	auditExportCmd.Flags().StringVar(&auditDBFlag, "db", "", "Path to SQLite database file (defaults to active Vraxter DB)")
	auditExportCmd.Flags().StringVar(&auditOutFlag, "out", "", "Output JSONL file path (required)")
	auditExportCmd.Flags().StringVar(&auditSinceFlag, "since", "", "Filter entries since RFC3339 timestamp")
	auditExportCmd.Flags().StringVar(&auditTraceIDFlag, "trace-id", "", "Filter entries by trace_id")

	auditCmd.AddCommand(auditVerifyCmd, auditExportCmd)
	rootCmd.AddCommand(auditCmd)
}
