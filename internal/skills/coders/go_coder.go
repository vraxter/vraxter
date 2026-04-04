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

type GoToolchain interface {
	GetGoPath() string
	IsReady(lang string) bool
	SetupSDK(lang string) error
}

type GoCoder struct {
	Repo      *db.SkillRepository
	TM        GoToolchain
	Runner    interfaces.SkillRunner
	SkillsDir string
}

func (c *GoCoder) Language() string { return "go" }

func (c *GoCoder) Compile(name, description, code string) error {
	if !c.TM.IsReady("go") {
		if err := c.TM.SetupSDK("go"); err != nil {
			return err
		}
	}

	skillID := strings.ReplaceAll(strings.ToLower(name), " ", "-")
	workDir := filepath.Join(os.TempDir(), "vraxter-go-build-"+skillID)
	os.MkdirAll(workDir, 0700)
	defer os.RemoveAll(workDir)

	srcPath := filepath.Join(workDir, "main.go")
	wasmPath := filepath.Join(c.SkillsDir, skillID+".wasm")

	code = strings.TrimSpace(code)

	startIdx := strings.Index(code, "```")
	if startIdx != -1 {
		openingTagEnd := strings.Index(code[startIdx:], "\n")
		if openingTagEnd == -1 {
			code = code[startIdx+3:]
		} else {
			code = code[startIdx+openingTagEnd+1:]
		}

		if endIdx := strings.Index(code, "```"); endIdx != -1 {
			code = code[:endIdx]
		}
	}
	code = strings.TrimSpace(code)

	if !strings.Contains(code, "package main") {
		if strings.Contains(code, "import ") && !strings.Contains(code, "\"") && !strings.Contains(code, "(") {
			return fmt.Errorf("code detected as non-Go (likely Python/Ruby). Please use Go (standard lib only)")
		}
		if strings.Contains(code, "def ") {
			return fmt.Errorf("code detected as Python (contains 'def'). Please provide Go implementation")
		} else {
			if !strings.Contains(code, "func main()") {
				code = "package main\n\nimport \"fmt\"\n\n" + code + "\n\nfunc main() {}\n"
			} else {
				code = "package main\n\nimport \"fmt\"\n\n" + code
			}
		}

		code = "package main\n\nimport \"fmt\"\n\n" + code
	} else {
		if pkgIdx := strings.Index(code, "package main"); pkgIdx > 0 {
			code = code[pkgIdx:]
		}
		if !strings.Contains(code, "func main()") {
			code = code + "\n\nfunc main() {}\n"
		}
	}

	log.Printf("GoCoder: Compiling skill '%s' (%d bytes). Preview: %.50s...", name, len(code), strings.ReplaceAll(code, "\n", " "))

	if err := os.WriteFile(srcPath, []byte(code), 0600); err != nil {
		return err
	}
	goPath := c.TM.GetGoPath()
	modCmd := exec.Command(goPath, "mod", "init", skillID)
	modCmd.Dir = workDir
	if out, err := modCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod init failed: %v | log: %s", err, string(out))
	}

	sdkPath := filepath.Join(workDir, "vrax.go")
	sdkCode := "package main\nimport \"unsafe\"\n//go:wasmimport vrax_v1 http_get\nfunc hostHttpGet(urlPtr, urlLen, outPtr, outMax uint32) uint32\n\nfunc HttpGet(url string) string {\n\tout := make([]byte, 8192)\n\tuBytes := []byte(url)\n\tuPtr := uint32(uintptr(unsafe.Pointer(&uBytes[0])))\n\toPtr := uint32(uintptr(unsafe.Pointer(&out[0])))\n\tn := hostHttpGet(uPtr, uint32(len(uBytes)), oPtr, 8192)\n\tif n == 0 { return \"\" }\n\treturn string(out[:n])\n}\n"

	if strings.Contains(code, "HttpGet") {
		os.WriteFile(sdkPath, []byte(sdkCode), 0600)
	}

	cmd := exec.Command(goPath, "build", "-o", wasmPath, ".")
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("go build failed: %v | log: %s", err, string(output))
	}

	checksum := ""
	if data, err := os.ReadFile(wasmPath); err == nil {
		h := sha256.Sum256(data)
		checksum = hex.EncodeToString(h[:])
	}

	manifest := types.SkillManifest{
		ID:          skillID,
		Name:        name,
		Description: description,
		Command:     wasmPath,
		Engine:      "wasm",
		Language:    "go",
		Version:     "1.0.0",
		Tier:        types.Tier2CommunityVerified,
		Checksum:    checksum,
	}

	// 7. Execution Dry-Run Verification (Failsafe)
	_, err = c.Runner.Execute(context.Background(), manifest, map[string]interface{}{"_vraxter_dry_run": true})
	if err != nil {
		os.Remove(wasmPath) // Purge corrupted binary
		return fmt.Errorf("skill compiled successfully but failed verification check (Dry-Run crashed): %v", err)
	}

	// TODO: Dispatch to Central Vraxter Hub 
	log.Printf("GoCoder: Skill verified! Persisting '%s' to database.", skillID)

	return c.Repo.UpsertSkill(manifest)
}
