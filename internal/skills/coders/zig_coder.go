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
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

type ZigToolchain interface {
	GetZigPath() string
	IsReady(lang string) bool
	SetupSDK(lang string) error
}

type ZigCoder struct {
	Repo      *db.SkillRepository
	TM        ZigToolchain
	Runner    interfaces.SkillRunner
	SkillsDir string
}

func (c *ZigCoder) Language() string { return "zig" }

func (c *ZigCoder) Compile(name, description, paramsSchema, code string, permissions []string) error {
	return c.CompileWithManifest(name, description, paramsSchema, code, permissions, nil)
}

func (c *ZigCoder) CompileWithManifest(name, description, paramsSchema, code string, permissions []string, manifestV1 *types.SkillManifestV1) error {
	if !c.TM.IsReady("zig") {
		if err := c.TM.SetupSDK("zig"); err != nil {
			return err
		}
	}

	skillID := sanitizeSkillID(name)
	workDir, err := os.MkdirTemp("", "vraxter-zig-build-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary Zig build directory: %w", err)
	}
	_ = os.Chmod(workDir, 0700)
	defer os.RemoveAll(workDir)

	srcPath := filepath.Join(workDir, "main.zig")
	wasmPath := filepath.Join(c.SkillsDir, skillID+".wasm")

	if err := os.WriteFile(srcPath, []byte(code), 0600); err != nil {
		return err
	}
	zigPath := c.TM.GetZigPath()
	cmd := exec.Command(zigPath, "build-exe", "-target", "wasm32-wasi", "-O", "ReleaseSmall", fmt.Sprintf("-femit-bin=%s", wasmPath), srcPath)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("zig compiler failed: %v | log: %s", err, string(output))
	}

	_ = os.Chmod(wasmPath, 0600)

	checksum := ""
	if data, err := os.ReadFile(wasmPath); err == nil {
		h := sha256.Sum256(data)
		checksum = hex.EncodeToString(h[:])
	}

	manifest := BuildAndSaveV1ManifestWithTemplate(c.SkillsDir, skillID, name, description, wasmPath, checksum, paramsSchema, "zig", permissions, manifestV1)

	// 6b. Static WASM bytecode import verification against declared manifest capabilities
	if _, statErr := os.Stat(wasmPath); statErr == nil {
		if err := skills.VerifyWASMFile(context.Background(), wasmPath, manifest); err != nil {
			_ = os.Remove(wasmPath)
			_ = os.Remove(filepath.Join(c.SkillsDir, skillID+".manifest.json"))
			return fmt.Errorf("skill failed static WASM capability verification: %w", err)
		}
	}

	// 7. Execution Dry-Run Verification (Failsafe in zero-capability sandbox)
	if dr, ok := c.Runner.(interfaces.DryRunSkillRunner); ok {
		_, err = dr.ExecuteDryRun(context.Background(), manifest)
	} else {
		_, err = c.Runner.Execute(context.Background(), dryRunSandboxManifest(manifest), map[string]interface{}{"_vraxter_dry_run": true})
	}
	if err != nil {
		_ = os.Remove(wasmPath) // Purge corrupted binary
		_ = os.Remove(filepath.Join(c.SkillsDir, skillID+".manifest.json"))
		return fmt.Errorf("skill compiled successfully but failed verification check (Dry-Run crashed): %v", err)
	}

	log.Printf("ZigCoder: Skill verified! Persisting '%s' to database.", skillID)

	return c.Repo.UpsertSkill(manifest)
}
