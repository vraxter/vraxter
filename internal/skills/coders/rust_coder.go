package coders

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/patagonicrune/vraxter/internal/db"
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
	SkillsDir string
}

func (c *RustCoder) Language() string { return "rust" }

func (c *RustCoder) Compile(name, description, code string) error {
	// 1. Prepare Tools
	if !c.TM.IsReady("rust") {
		if err := c.TM.SetupSDK("rust"); err != nil {
			return err
		}
	}

	// 2. Prepare Paths
	skillID := strings.ReplaceAll(strings.ToLower(name), " ", "-")
	workDir := filepath.Join(os.TempDir(), "vraxter-rust-build-"+skillID)
	os.MkdirAll(workDir, 0700)
	defer os.RemoveAll(workDir)

	srcPath := filepath.Join(workDir, "main.rs")
	wasmPath := filepath.Join(c.SkillsDir, skillID+".wasm")

	// 3. Write Source
	if err := os.WriteFile(srcPath, []byte(code), 0600); err != nil {
		return err
	}

	// 4. Compile: rustc --target wasm32-wasip1 -o output.wasm main.rs
	rustcPath := c.TM.GetRustcPath()
	cmd := exec.Command(rustcPath, "--target", "wasm32-wasip1", "-o", wasmPath, srcPath)
	
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("rustc failed: %v | log: %s", err, string(output))
	}

	// 5. Calculate Checksum (Vraxter Shield — mandatory integrity baseline)
	checksum := ""
	if data, err := os.ReadFile(wasmPath); err == nil {
		h := sha256.Sum256(data)
		checksum = hex.EncodeToString(h[:])
	}

	// 6. Register
	manifest := types.SkillManifest{
		ID:          skillID,
		Name:        name,
		Description: description,
		Command:     wasmPath,
		Engine:      "wasm",
		Language:    "rust",
		Version:     "1.0.0",
		Tier:        types.Tier2CommunityVerified,
		Checksum:    checksum,
	}

	return c.Repo.UpsertSkill(manifest)
}
