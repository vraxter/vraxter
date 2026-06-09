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
	"strings"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
	"github.com/patagonicrune/vraxter/pkg/types"
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

func (c *RustCoder) Compile(name, description, paramsSchema, code string) error {
	if !c.TM.IsReady("rust") {
		if err := c.TM.SetupSDK("rust"); err != nil {
			return err
		}
	}

	skillID := strings.ReplaceAll(strings.ToLower(name), " ", "-")
	workDir := filepath.Join(os.TempDir(), "vraxter-rust-build-"+skillID)
	os.MkdirAll(workDir, 0700)
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

	checksum := ""
	if data, err := os.ReadFile(wasmPath); err == nil {
		h := sha256.Sum256(data)
		checksum = hex.EncodeToString(h[:])
	}

	 manifest := types.SkillManifest{
		ID:           skillID,
		Name:         name,
		Description:  description,
		ParamsSchema: paramsSchema,
		Command:      wasmPath,
		Engine:       "wasm",
		Language:     "rust",
		Version:      "1.0.0",
		Tier:         types.Tier2CommunityVerified,
		Checksum:     checksum,
	}

	// 7. Execution Dry-Run Verification (Failsafe)
	_, err = c.Runner.Execute(context.Background(), manifest, map[string]interface{}{"_vraxter_dry_run": true})
	if err != nil {
		os.Remove(wasmPath) // Purge corrupted binary
		return fmt.Errorf("skill compiled successfully but failed verification check (Dry-Run crashed): %v", err)
	}

	// TODO: Dispatch to Central Vraxter Hub 
	log.Printf("RustCoder: Skill verified! Persisting '%s' to database.", skillID)

	return c.Repo.UpsertSkill(manifest)
}
