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
	"os/exec"
	"sync"
	"time"

	"github.com/patagonicrune/vraxter/pkg/types"
	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// Runner manages the execution of local skills via IPC (Stdin/Stdout/WASM)
type Runner struct {
	Timeout time.Duration
	
	// Performance Optimization: Reusable runtime and module cache
	runtime wazero.Runtime
	cache   map[string]wazero.CompiledModule
	mu      sync.RWMutex
}

// NewRunner creates a new Runner scoped to a timeout
func NewRunner() *Runner {
	ctx := context.Background()
	r := wazero.NewRuntime(ctx)
	
	// Pre-instantiate WASI into the runtime
	wasi_snapshot_preview1.MustInstantiate(ctx, r)

	// --- Vraxter Power Host functions (vrax_v1) ---
	_, err := r.NewHostModuleBuilder("vrax_v1").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, mod api.Module, urlPtr, urlLen, outPtr, outMax uint32) uint32 {
			urlBytes, ok := mod.Memory().Read(urlPtr, urlLen)
			if !ok { return 0 }
			url := string(urlBytes)

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Get(url)
			if err != nil {
				return 0
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			if uint32(len(body)) > outMax {
				body = body[:outMax] // Truncate to buffer size
			}
			mod.Memory().Write(outPtr, body)
			return uint32(len(body))
		}).
		Export("http_get").
		Instantiate(ctx)
	if err != nil {
		fmt.Printf("Warning: Failed to initialize Vraxter V1 host functions: %v\n", err)
	}

	return &Runner{
		Timeout: 60 * time.Second,
		runtime: r,
		cache:   make(map[string]wazero.CompiledModule),
	}
}

// Close releases resources held by the runner
func (rn *Runner) Close(ctx context.Context) error {
	return rn.runtime.Close(ctx)
}

// Execute handles the environment selection logic based on the skill Manifest
func (rn *Runner) Execute(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}) (*types.ExecutionResult, error) {
	if manifest.Engine == "wasm" || manifest.Engine == "" {
		return rn.executeWasm(ctx, manifest, params)
	}
	// Fallback to absolute OS level binary execution (Dangerous: Requires Trust)
	return rn.executeNative(ctx, manifest, params)
}

// executeNative runs raw OS binaries.
func (rn *Runner) executeNative(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}) (*types.ExecutionResult, error) {
	reqPayload := types.SkillRequest{
		JSONRPC: "2.0",
		Method:  "execute",
		Params:  params,
		ID:      "1", // TODO: Use real IDs or from metadata
	}

	inputBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode intent payload: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, rn.Timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, manifest.Command)

	cmd.Stdin = bytes.NewReader(inputBytes)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("skill execution timed out")
		}
		return nil, fmt.Errorf("skill execution failed: %v, stderr: %s", err, stderr.String())
	}

	var response types.SkillResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("failed to parse skill response payload: %w. Raw STDOUT: %s", err, stdout.String())
	}

	if response.Error != nil {
		return nil, fmt.Errorf("skill returned RPC error: code %d, message: %s", response.Error.Code, response.Error.Message)
	}

	if response.Result == nil {
		return nil, fmt.Errorf("malformed response: no result payload")
	}

	return response.Result, nil
}

// executeWasm handles executing .wasm sandboxed logic using Tetrate Labs Wazero engine
func (rn *Runner) executeWasm(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}) (*types.ExecutionResult, error) {
	reqPayload := types.SkillRequest{
		JSONRPC: "2.0",
		Method:  "execute",
		Params:  params,
		ID:      "1",
	}

	inputBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to encode intent payload: %w", err)
	}

	execCtx, cancel := context.WithTimeout(ctx, rn.Timeout)
	defer cancel()

	// 1. INTEGRITY CHECK (Vraxter Shield - Phase 12.10)
	// We MUST ensure the file on disk matches what's in the DB
	currentHash, err := rn.calculateHash(manifest.Command)
	if err != nil {
		return nil, fmt.Errorf("shield: failed to verify binary integrity: %w", err)
	}
	if manifest.Checksum != "" && currentHash != manifest.Checksum {
		return nil, fmt.Errorf("CRITICAL SECURITY ALERT: Binary %s integrity verification FAILED. Expected %s, got %s. Possible hijacking or corruption detected", manifest.ID, manifest.Checksum, currentHash)
	}

	// 2. PERMISSION SHIELD (Autonomous Trust v2)
	// Priority: Official > Reputation (High score/downloads) > Sandbox Safety
	isOfficial := manifest.IsOfficial
	hasHighReputation := manifest.Score >= 4.0 && manifest.Downloads > 100
	isSandboxSafe := len(manifest.Permissions) == 0

	isAuthorized := isOfficial || hasHighReputation || isSandboxSafe

	if !isAuthorized && manifest.Tier >= types.Tier3Unverified {
		return nil, fmt.Errorf("VRAXTER SHIELD: Skill '%s' requires specialized permissions and lacks community trust. To allow this skill, run 'vraxter skills trust %s'", manifest.ID, manifest.ID)
	}

	// 2. Get or Compile Module (Caching for Performance)
	rn.mu.RLock()
	compiled, ok := rn.cache[manifest.Command]
	rn.mu.RUnlock()

	if !ok {
		wasmBytes, err := os.ReadFile(manifest.Command)
		if err != nil {
			return nil, fmt.Errorf("failed to read WASM isolation binary %s: %w", manifest.Command, err)
		}

		compiled, err = rn.runtime.CompileModule(execCtx, wasmBytes)
		if err != nil {
			return nil, fmt.Errorf("failed to compile WASM module: %w", err)
		}

		rn.mu.Lock()
		rn.cache[manifest.Command] = compiled
		rn.mu.Unlock()
	}

	var stdout, stderr bytes.Buffer
	config := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(inputBytes)).
		WithStdout(&stdout).
		WithStderr(&stderr)

	// 2. Instantiate and Execute instantly
	_, err = rn.runtime.InstantiateModule(execCtx, compiled, config)
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("wasm skill execution timed out")
		}
		return nil, fmt.Errorf("wasm skill crashed during execution: %v, stderr: %s", err, stderr.String())
	}

	// 3. Return Output and Interpret
	var response types.SkillResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("failed to parse WASM standard output: %w. Raw STDOUT: %s", err, stdout.String())
	}

	if response.Error != nil {
		return nil, fmt.Errorf("wasm skill returned logical error: code %d, message: %s", response.Error.Code, response.Error.Message)
	}

	if response.Result == nil {
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
