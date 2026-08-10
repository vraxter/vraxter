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
	if !c.TM.IsReady("zig") {
		if err := c.TM.SetupSDK("zig"); err != nil {
			return err
		}
	}

	skillID := strings.ReplaceAll(strings.ToLower(name), " ", "-")
	workDir := filepath.Join(os.TempDir(), "vraxter-zig-build-"+skillID)
	os.MkdirAll(workDir, 0700)
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
		Language:     "zig",
		Version:      "1.0.0",
		Tier:         types.Tier2CommunityVerified,
		Checksum:     checksum,
		Permissions:  permissions,
	}

	// 7. Execution Dry-Run Verification (Failsafe)
	_, err = c.Runner.Execute(context.Background(), manifest, map[string]interface{}{"_vraxter_dry_run": true})
	if err != nil {
		os.Remove(wasmPath) // Purge corrupted binary
		return fmt.Errorf("skill compiled successfully but failed verification check (Dry-Run crashed): %v", err)
	}

	log.Printf("ZigCoder: Skill verified! Persisting '%s' to database.", skillID)

	return c.Repo.UpsertSkill(manifest)
}
