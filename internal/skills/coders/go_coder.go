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
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

// GoHTTPSDKCode is injected into compiled Go WASM skills that reference HttpGet.
// It imports "vrax_sandbox"."http_get" matching internal/skills/runner.go and guards
// against empty URL slices and out-of-bounds host return values.
const GoHTTPSDKCode = `package main

import (
	"runtime"
	"unsafe"
)

//go:wasmimport vrax_sandbox http_get
func hostHttpGet(urlPtr, urlLen, outPtr, outMax uint32) uint32

func HttpGet(url string) string {
	if len(url) == 0 {
		return ""
	}
	out := make([]byte, 8192)
	uBytes := []byte(url)
	uPtr := uint32(uintptr(unsafe.Pointer(&uBytes[0])))
	oPtr := uint32(uintptr(unsafe.Pointer(&out[0])))
	n := hostHttpGet(uPtr, uint32(len(uBytes)), oPtr, uint32(len(out)))
	runtime.KeepAlive(uBytes)
	runtime.KeepAlive(out)
	if n == 0 || int(n) > len(out) {
		return ""
	}
	return string(out[:n])
}
`

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
	return c.CompileWithManifest(name, description, paramsSchema, code, permissions, nil)
}

func (c *GoCoder) CompileWithManifest(name, description, paramsSchema, code string, permissions []string, manifestV1 *types.SkillManifestV1) error {
	if !c.TM.IsReady("go") {
		if err := c.TM.SetupSDK("go"); err != nil {
			return err
		}
	}

	skillID := sanitizeSkillID(name)
	workDir, err := os.MkdirTemp("", "vraxter-go-build-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary Go build directory: %w", err)
	}
	_ = os.Chmod(workDir, 0700)
	defer os.RemoveAll(workDir)

	srcPath := filepath.Join(workDir, "main.go")
	tmpWasmPath := filepath.Join(workDir, skillID+".wasm")
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
	modCmd.Env = SanitizedGoBuildEnv()
	if out, err := modCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go mod init failed: %v | log: %s", err, string(out))
	}

	sdkPath := filepath.Join(workDir, "vrax.go")
	if strings.Contains(code, "HttpGet") {
		if err := os.WriteFile(sdkPath, []byte(GoHTTPSDKCode), 0600); err != nil {
			return fmt.Errorf("failed to write Vraxter Go HTTP SDK shim: %w", err)
		}
	}

	var cmd *exec.Cmd
	var isTinyGo bool
	if c.TM.IsReady("tinygo") {
		tinyGoPath := c.TM.GetTinyGoPath()
		cmd = exec.Command(tinyGoPath, "build", "-o", tmpWasmPath, "-target=wasi", ".")
		cmd.Env = SanitizedGoBuildEnv()
		isTinyGo = true
	} else {
		goPath := c.TM.GetGoPath()
		cmd = exec.Command(goPath, "build", "-o", tmpWasmPath, ".")
		cmd.Env = SanitizedGoBuildEnv("GOOS=wasip1", "GOARCH=wasm")
	}

	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()

	// If TinyGo fails (e.g., unsupported stdlib package like full 'net/http'), fallback automatically to standard Go!
	if err != nil && isTinyGo {
		log.Printf("GoCoder: TinyGo compilation failed, falling back to standard Go compiler. Reason: %v", err)
		goPath := c.TM.GetGoPath()
		cmd = exec.Command(goPath, "build", "-o", tmpWasmPath, ".")
		cmd.Env = SanitizedGoBuildEnv("GOOS=wasip1", "GOARCH=wasm")
		cmd.Dir = workDir
		output, err = cmd.CombinedOutput()
	}

	if err != nil {
		return fmt.Errorf("build failed: %v | log: %s", err, string(output))
	}

	if _, statErr := os.Stat(tmpWasmPath); statErr == nil {
		if err := utils.AtomicCopyFileBounded(tmpWasmPath, wasmPath, skills.MaxWASMBinaryBytes, 0600); err != nil {
			return fmt.Errorf("failed atomically installing compiled WASM binary: %w", err)
		}
	}

	checksum := ""
	if data, err := os.ReadFile(wasmPath); err == nil {
		h := sha256.Sum256(data)
		checksum = hex.EncodeToString(h[:])
	}

	manifest := BuildAndSaveV1ManifestWithTemplate(c.SkillsDir, skillID, name, description, wasmPath, checksum, paramsSchema, "go", permissions, manifestV1)

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

	// Technical success logged to vraxter.log
	log.Printf("GoCoder: Skill verified! Persisting '%s' to database.", skillID)

	return c.Repo.UpsertSkill(manifest)
}

// SanitizedGoBuildEnv constructs a minimal, secret-free environment for offline Go/TinyGo
// WASM skill compilation. It strictly drops arbitrary host environment variables (API keys,
// cloud credentials, tokens) and pins CGO_ENABLED=0, GOPROXY=off, and GOSUMDB=off.
func SanitizedGoBuildEnv(extra ...string) []string {
	allowedKeys := []string{
		"PATH",
		"HOME",
		"USERPROFILE",
		"HOMEDRIVE",
		"HOMEPATH",
		"APPDATA",
		"LOCALAPPDATA",
		"SYSTEMROOT",
		"WINDIR",
		"COMSPEC",
		"TMP",
		"TEMP",
		"TMPDIR",
		"GOROOT",
		"GOPATH",
		"GOCACHE",
		"GOMODCACHE",
		"GOTOOLCHAIN",
	}

	env := make([]string, 0, len(allowedKeys)+len(extra)+4)
	for _, k := range allowedKeys {
		if v, ok := os.LookupEnv(k); ok && v != "" {
			env = append(env, k+"="+v)
		}
	}
	env = append(env,
		"CGO_ENABLED=0",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTELEMETRY=off",
	)
	env = append(env, extra...)
	return env
}
