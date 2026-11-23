// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package coders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

type RustToolchain interface {
	GetRustcPath() string
	IsReady(lang string) bool
	SetupSDK(lang string) error
}

type RustCoder struct {
	Repo      *db.SkillRepository
	TM        RustToolchain
	Runner    interfaces.SkillRunner
	SkillsDir string
}

func (c *RustCoder) Language() string { return "rust" }

func (c *RustCoder) Compile(name, description, paramsSchema, code string, permissions []string) error {
	return c.CompileWithManifest(name, description, paramsSchema, code, permissions, nil)
}

func (c *RustCoder) CompileWithManifest(name, description, paramsSchema, code string, permissions []string, manifestV1 *types.SkillManifestV1) error {
	if !c.TM.IsReady("rust") {
		if err := c.TM.SetupSDK("rust"); err != nil {
			return err
		}
	}

	skillID := sanitizeSkillID(name)
	workDir, err := os.MkdirTemp("", "vraxter-rust-build-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary Rust build directory: %w", err)
	}
	_ = os.Chmod(workDir, 0700)
	defer os.RemoveAll(workDir)

	srcPath := filepath.Join(workDir, "main.rs")
	wasmPath := filepath.Join(c.SkillsDir, skillID+".wasm")

	if err := os.WriteFile(srcPath, []byte(code), 0600); err != nil {
		return err
	}
	rustcPath := c.TM.GetRustcPath()
	cmd := exec.Command(rustcPath, "--target", "wasm32-wasip1", "-o", wasmPath, srcPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rustc failed: %v | log: %s", err, string(output))
	}

	_ = os.Chmod(wasmPath, 0600)

	checksum := ""
	if data, err := os.ReadFile(wasmPath); err == nil {
		h := sha256.Sum256(data)
		checksum = hex.EncodeToString(h[:])
	}

	manifest := BuildAndSaveV1ManifestWithTemplate(c.SkillsDir, skillID, name, description, wasmPath, checksum, paramsSchema, "rust", permissions, manifestV1)

	// 7. Execution Dry-Run Verification (Failsafe in zero-capability sandbox)
	_, err = c.Runner.Execute(context.Background(), dryRunSandboxManifest(manifest), map[string]interface{}{"_vraxter_dry_run": true})
	if err != nil {
		_ = os.Remove(wasmPath) // Purge corrupted binary
		_ = os.Remove(filepath.Join(c.SkillsDir, skillID+".manifest.json"))
		return fmt.Errorf("skill compiled successfully but failed verification check (Dry-Run crashed): %v", err)
	}

	// NOTE: Future integration point for publishing compiled modules to the Vraxter Skill Hub.
	log.Printf("RustCoder: Skill verified! Persisting '%s' to database.", skillID)

	return c.Repo.UpsertSkill(manifest)
}
