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
	"regexp"
	"strings"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

type GoToolchain interface {
	GetGoPath() string
	GetTinyGoPath() string
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

func (c *GoCoder) Compile(name, description, paramsSchema, code string, permissions []string) error {
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

	// Clean out markdown blocks using regex
	re := regexp.MustCompile(`(?s)(?:` + "```" + `(?:go)?\s*)(.*?)(?:` + "```" + `)`)
	matches := re.FindStringSubmatch(code)
	if len(matches) > 1 {
		code = matches[1]
	} else {
		// Fallback: Manually trim markdown backticks if regex didn't trigger
		if strings.HasPrefix(code, "```") {
			lines := strings.Split(code, "\n")
			if len(lines) > 2 {
				code = strings.Join(lines[1:len(lines)-1], "\n")
			}
		}
	}
	code = strings.TrimSpace(code)

	// Language detection sanity checks
	if strings.Contains(code, "def ") && !strings.Contains(code, "func ") {
		return fmt.Errorf("code detected as Python (contains 'def'). Please provide Go implementation")
	}

	// Normalizing the main package structure
	hasPackageMain := strings.Contains(code, "package main")
	hasFuncMain := strings.Contains(code, "func main()")

	if !hasPackageMain {
		code = "package main\n\n" + code
	} else {
		if pkgIdx := strings.Index(code, "package main"); pkgIdx > 0 {
			code = code[pkgIdx:]
		}
	}

	if !hasFuncMain {
		code = code + "\n\nfunc main() {}\n"
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

	var cmd *exec.Cmd
	var isTinyGo bool
	if c.TM.IsReady("tinygo") {
		tinyGoPath := c.TM.GetTinyGoPath()
		cmd = exec.Command(tinyGoPath, "build", "-o", wasmPath, "-target=wasi", ".")
		isTinyGo = true
	} else {
		goPath := c.TM.GetGoPath()
		cmd = exec.Command(goPath, "build", "-o", wasmPath, ".")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	}

	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()

	// If TinyGo fails (e.g., unsupported stdlib package like full 'net/http'), fallback automatically to standard Go!
	if err != nil && isTinyGo {
		log.Printf("GoCoder: TinyGo compilation failed, falling back to standard Go compiler. Reason: %v", err)
		goPath := c.TM.GetGoPath()
		cmd = exec.Command(goPath, "build", "-o", wasmPath, ".")
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		cmd.Dir = workDir
		output, err = cmd.CombinedOutput()
	}

	if err != nil {
		return fmt.Errorf("build failed: %v | log: %s", err, string(output))
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
		Language:     "go",
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

	// Technical success logged to vraxter.log
	log.Printf("GoCoder: Skill verified! Persisting '%s' to database.", skillID)

	return c.Repo.UpsertSkill(manifest)
}
