// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/vraxter/vraxter/pkg/types"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

type Runner struct {
	Timeout time.Duration
	runtime wazero.Runtime
	cache   map[string]wazero.CompiledModule
	mu      sync.RWMutex
}

func NewRunner() *Runner {
	ctx := context.Background()

	// Hardening: 128MB Memory Limit (2048 pages * 64KB) and strict CPU bounds.
	config := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(2048).
		WithCloseOnContextDone(true)

	r := wazero.NewRuntimeWithConfig(ctx, config)

	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	if err := createHostBuilder(r, ctx); err != nil {
		fmt.Printf("Warning: Failed to initialize Vraxter V1 host functions: %v\n", err)
	}

	rn := &Runner{
		Timeout: 60 * time.Second,
		runtime: r,
		cache:   make(map[string]wazero.CompiledModule),
	}
	return rn
}

type ctxKey string
const manifestKey ctxKey = "vrax_manifest"

func httpGetFn(ctx context.Context, mod api.Module, urlPtr, urlLen, outPtr, outMax uint32) uint32 {
	// Retrieve the manifest from the context to verify capabilities
	manifestVal := ctx.Value(manifestKey)
	if manifestVal == nil {
		// Secure by default: Abort if manifest is not propagated
		return 0
	}
	manifest, ok := manifestVal.(types.SkillManifest)
	if !ok {
		return 0
	}

	// Verify the "network" capability exists in permissions
	hasNetwork := false
	for _, perm := range manifest.Permissions {
		if perm == "network" {
			hasNetwork = true
			break
		}
	}

	if !hasNetwork {
		fmt.Printf("\n[SHIELD SECURITY ALERT] Skill '%s' tried to make an outbound HTTP request but lacks the 'network' permission capability. Blocked.\n", manifest.ID)
		return 0
	}

	urlBytes, ok := mod.Memory().Read(urlPtr, urlLen)
	if !ok {
		return 0
	}
	url := string(urlBytes)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if uint32(len(body)) > outMax {
		body = body[:outMax]
	}
	mod.Memory().Write(outPtr, body)
	return uint32(len(body))
}

func createHostBuilder(rt wazero.Runtime, ctx context.Context) error {
	_, err := rt.NewHostModuleBuilder("vrax_sandbox").
		NewFunctionBuilder().
		WithFunc(httpGetFn).
		Export("http_get").
		Instantiate(ctx)

	return err
}

func (rn *Runner) Close(ctx context.Context) error {
	return rn.runtime.Close(ctx)
}
func (rn *Runner) Execute(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}) (*types.ExecutionResult, error) {
	if manifest.Engine != "wasm" && manifest.Engine != "" {
		return nil, fmt.Errorf("skill engine '%s' is not supported (only wasm is allowed)", manifest.Engine)
	}
	return rn.executeWasm(ctx, manifest, params)
}

func (rn *Runner) executeWasm(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}) (*types.ExecutionResult, error) {
	reqPayload := types.SkillRequest{
		JSONRPC: "2.0",
		Method:  "execute",
		Params:  params,
		ID:      manifest.ID,
	}

	inputBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode intent payload: %w", err)
	}

	timeout := rn.Timeout
	if manifest.TimeoutSeconds > 0 {
		timeout = time.Duration(manifest.TimeoutSeconds) * time.Second
	}

	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	execCtx = context.WithValue(execCtx, manifestKey, manifest)

	currentHash, err := rn.calculateHash(manifest.Command)
	if err != nil {
		return nil, fmt.Errorf("shield: failed to verify binary integrity: %w", err)
	}
	if manifest.Checksum != "" && currentHash != manifest.Checksum {
		return nil, fmt.Errorf(
			"CRITICAL SECURITY ALERT: Binary %s integrity verification FAILED. "+
				"Expected %s, got %s. Possible hijacking or corruption detected",
			manifest.ID,
			manifest.Checksum,
			currentHash,
		)
	}

	isOfficial := manifest.IsOfficial
	hasHighReputation := manifest.Score >= 4.0 && manifest.Downloads > 100
	isSandboxSafe := len(manifest.Permissions) == 0

	isAuthorized := isOfficial || hasHighReputation || isSandboxSafe

	if !isAuthorized && manifest.Tier >= types.Tier3Unverified {
		return nil, fmt.Errorf(
			"VRAXTER SHIELD: Skill '%s' requires specialized permissions and lacks community trust. "+
				"To allow this skill, run 'vraxter skills trust %s'", manifest.ID, manifest.ID)
	}

	rn.mu.RLock()
	compiled, ok := rn.cache[manifest.Command]
	rn.mu.RUnlock()

	if !ok {
		wasmBytes, err := os.ReadFile(manifest.Command)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to read WASM isolation binary %s: %w",
				manifest.Command,
				err,
			)
		}

		compiled, err = rn.runtime.CompileModule(execCtx, wasmBytes)
		if err != nil {
			return nil, fmt.Errorf(
				"failed to compile WASM module: %w",
				err,
			)
		}

		rn.mu.Lock()
		rn.cache[manifest.Command] = compiled
		rn.mu.Unlock()
	}

	fsConfig := wazero.NewFSConfig()
	for _, perm := range manifest.Permissions {
		if strings.HasPrefix(perm, "fs_read:") {
			dir := strings.TrimPrefix(perm, "fs_read:")
			resolvedDir := ResolveMountPath(dir)
			if resolvedDir != "" {
				fsConfig = fsConfig.WithReadOnlyDirMount(resolvedDir, resolvedDir)
			}
		} else if strings.HasPrefix(perm, "fs_write:") {
			dir := strings.TrimPrefix(perm, "fs_write:")
			resolvedDir := ResolveMountPath(dir)
			if resolvedDir != "" {
				fsConfig = fsConfig.WithDirMount(resolvedDir, resolvedDir)
			}
		}
	}

	var stdout, stderr bytes.Buffer
	config := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(inputBytes)).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithEnv("PATH", "").
		WithFSConfig(fsConfig)

	mod, err := rn.runtime.InstantiateModule(execCtx, compiled, config)
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("wasm skill execution timed out")
		}
		return nil, fmt.Errorf(
			"wasm skill crashed during execution: %v, stderr: %s",
			err,
			stderr.String(),
		)
	}
	defer mod.Close(execCtx)

	var response types.SkillResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		// FALLBACK: If the WASM tool outputs raw text or invalid JSONRPC, gracefully wrap it!
		rawStr := strings.TrimSpace(stdout.String())
		if rawStr != "" {
			return &types.ExecutionResult{
				Status: "completed",
				Output: rawStr,
			}, nil
		}
		
		return nil, fmt.Errorf(
			"failed to parse WASM standard output and STDOUT was empty. Raw err: %w",
			err,
		)
	}

	if response.Error != nil {
		return nil, fmt.Errorf(
			"wasm skill returned logical error: code %d, message: %s",
			response.Error.Code,
			response.Error.Message,
		)
	}

	if response.Result == nil {
		// FALLBACK for properly parsed JSON but missing root payload
		rawStr := strings.TrimSpace(stdout.String())
		if rawStr != "" {
			return &types.ExecutionResult{
				Status: "completed",
				Output: rawStr,
			}, nil
		}
		return nil, fmt.Errorf("wasm response format malformation: missing root payload")
	}

	return response.Result, nil
}

func (rn *Runner) calculateHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

func ResolveMountPath(path string) string {
	cleaned := strings.TrimSpace(path)
	if cleaned == "" {
		return ""
	}

	// 1. Expand environment variables
	cleaned = os.ExpandEnv(cleaned)

	// 2. Expand home directory shortcuts in a cross-platform manner
	home, err := os.UserHomeDir()
	if err == nil {
		if strings.HasPrefix(cleaned, "~") {
			cleaned = filepath.Join(home, strings.TrimPrefix(cleaned, "~"))
		} else if strings.HasPrefix(cleaned, "$HOME") {
			cleaned = filepath.Join(home, strings.TrimPrefix(cleaned, "$HOME"))
		} else if strings.HasPrefix(cleaned, "%USERPROFILE%") {
			cleaned = filepath.Join(home, strings.TrimPrefix(cleaned, "%USERPROFILE%"))
		}
	}

	// 3. Obtain native absolute path
	abs, err := filepath.Abs(cleaned)
	if err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(cleaned)
}
