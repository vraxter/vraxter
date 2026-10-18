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
	crand "crypto/rand"
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

	"github.com/vraxter/vraxter/internal/utils"
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

	// Verify the "network" capability exists in V1 capabilities or legacy permissions
	hasNetwork := false
	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil && manifest.ManifestV1.Capabilities.Network != nil {
		netCap := manifest.ManifestV1.Capabilities.Network
		if netCap.Mode != "" && strings.ToLower(netCap.Mode) != "none" {
			hasNetwork = true
		}
	}

	if !hasNetwork {
		for _, perm := range manifest.Permissions {
			p := strings.ToLower(strings.TrimSpace(perm))
			if p == "network" || strings.HasPrefix(p, "network:") || strings.HasPrefix(p, "net:") {
				hasNetwork = true
				break
			}
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

	// Enforce Airgap Network Shield: Verify destination URL in-memory without DNS leaks
	allowed, err := utils.IsAllowedNetwork(url, nil)
	if err != nil || !allowed {
		fmt.Printf("\n[SHIELD SECURITY ALERT] Skill '%s' outbound HTTP request to '%s' blocked by Air-Gap Shield (Zero-Egress): %v\n", manifest.ID, url, err)
		return 0
	}

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

	timeout := manifest.GetExecutionTimeout(rn.Timeout)
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
	isSandboxSafe := len(manifest.Permissions) == 0 && (manifest.ManifestV1 == nil || manifest.ManifestV1.Capabilities == nil)

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

	type activeMount struct {
		guest string
		host  string
		mode  string
	}
	var activeMounts []activeMount

	runTempDir, err := os.MkdirTemp("", "vraxter-run-"+manifest.ID+"-*")
	if err == nil {
		defer os.RemoveAll(runTempDir)
	}

	fsConfig := wazero.NewFSConfig()

	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil && manifest.ManifestV1.Capabilities.FS != nil && len(manifest.ManifestV1.Capabilities.FS.Mounts) > 0 {
		for _, mount := range manifest.ManifestV1.Capabilities.FS.Mounts {
			var hostBase string
			switch strings.ToLower(mount.Source) {
			case "workspace":
				hostBase, _ = os.Getwd()
				if hostBase == "" {
					hostBase = "."
				}
			case "input":
				if inDir, ok := params["_input_dir"].(string); ok && inDir != "" {
					hostBase = inDir
				} else if inDir, ok := params["input_dir"].(string); ok && inDir != "" {
					hostBase = inDir
				} else {
					hostBase = filepath.Join(runTempDir, "input")
				}
			case "output":
				userHome, _ := os.UserHomeDir()
				hostBase = filepath.Join(userHome, ".vraxter", "artifacts", "skills", manifest.ID)
			case "skill_data":
				userHome, _ := os.UserHomeDir()
				hostBase = filepath.Join(userHome, ".vraxter", "skills", "data", manifest.ID)
			case "ephemeral":
				safeGuest := strings.ReplaceAll(mount.Guest, "/", "_")
				hostBase = filepath.Join(runTempDir, "ephemeral"+safeGuest)
			case "host":
				hostBase = ResolveMountPath(mount.HostPath)
			default:
				hostBase = filepath.Join(runTempDir, "storage_"+mount.Source)
			}

			hostDir := hostBase
			if mount.Subpath != "" {
				hostDir = filepath.Join(hostBase, mount.Subpath)
			}
			_ = os.MkdirAll(hostDir, 0755)

			if strings.ToLower(mount.Mode) == "ro" {
				fsConfig = fsConfig.WithReadOnlyDirMount(hostDir, mount.Guest)
			} else {
				fsConfig = fsConfig.WithDirMount(hostDir, mount.Guest)
			}

			activeMounts = append(activeMounts, activeMount{
				guest: mount.Guest,
				host:  hostDir,
				mode:  mount.Mode,
			})
		}
	} else {
		// Fallback for legacy permission strings
		for _, perm := range manifest.Permissions {
			p := strings.TrimSpace(perm)
			low := strings.ToLower(p)
			if strings.HasPrefix(low, "fs_read:") || strings.HasPrefix(low, "fs:read:") || strings.HasPrefix(low, "filesystem:read:") {
				idx := strings.Index(p, ":")
				if strings.HasPrefix(low, "fs:read:") || strings.HasPrefix(low, "filesystem:read:") {
					idx = strings.LastIndex(p, ":")
				}
				dir := strings.TrimSpace(p[idx+1:])
				resolvedDir := ResolveMountPath(dir)
				if resolvedDir != "" {
					fsConfig = fsConfig.WithReadOnlyDirMount(resolvedDir, resolvedDir)
					activeMounts = append(activeMounts, activeMount{guest: resolvedDir, host: resolvedDir, mode: "ro"})
				}
			} else if strings.HasPrefix(low, "fs_write:") || strings.HasPrefix(low, "fs:write:") || strings.HasPrefix(low, "filesystem:write:") {
				idx := strings.Index(p, ":")
				if strings.HasPrefix(low, "fs:write:") || strings.HasPrefix(low, "filesystem:write:") {
					idx = strings.LastIndex(p, ":")
				}
				dir := strings.TrimSpace(p[idx+1:])
				resolvedDir := ResolveMountPath(dir)
				if resolvedDir != "" {
					fsConfig = fsConfig.WithDirMount(resolvedDir, resolvedDir)
					activeMounts = append(activeMounts, activeMount{guest: resolvedDir, host: resolvedDir, mode: "rw"})
				}
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

	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil {
		caps := manifest.ManifestV1.Capabilities
		if caps.Clock != nil {
			if caps.Clock.Wall {
				config = config.WithSysWalltime()
			}
			if caps.Clock.Monotonic {
				config = config.WithSysNanotime()
			}
		}
		if caps.Random != nil && *caps.Random {
			config = config.WithRandSource(crand.Reader)
		}
		if caps.Env != nil && len(caps.Env.Vars) > 0 {
			for k, v := range caps.Env.Vars {
				config = config.WithEnv(k, v)
			}
		}
	}

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

	// Check if output is via file
	if manifest.ManifestV1 != nil && manifest.ManifestV1.IO != nil && strings.ToLower(manifest.ManifestV1.IO.Output.Via) == "file" {
		outGuestPath := manifest.ManifestV1.IO.Output.Path
		var matchedMount *activeMount
		longestPrefix := -1
		for i := range activeMounts {
			m := &activeMounts[i]
			gPrefix := m.guest
			if outGuestPath == gPrefix || strings.HasPrefix(outGuestPath, strings.TrimSuffix(gPrefix, "/")+"/") {
				if len(gPrefix) > longestPrefix {
					longestPrefix = len(gPrefix)
					matchedMount = m
				}
			}
		}

		if matchedMount == nil {
			return nil, fmt.Errorf("skill '%s' was configured with file output at '%s', but no mount matches that guest path", manifest.ID, outGuestPath)
		}

		relPath := strings.TrimPrefix(outGuestPath, strings.TrimSuffix(matchedMount.guest, "/"))
		relPath = strings.TrimPrefix(relPath, "/")
		hostFilePath := filepath.Join(matchedMount.host, relPath)

		data, err := os.ReadFile(hostFilePath)
		if err != nil {
			return nil, fmt.Errorf("skill '%s' finished execution but failed to read expected output file at guest '%s' (host: %s): %w", manifest.ID, outGuestPath, hostFilePath, err)
		}

		return &types.ExecutionResult{
			IntentID: manifest.ID,
			Status:   types.StatusCompleted,
			Output:   string(data),
		}, nil
	}

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
