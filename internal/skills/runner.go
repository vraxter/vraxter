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
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/types"
)

const (
	// MaxWASMBinaryBytes caps the maximum .wasm binary size loaded or hashed into memory (50 MB).
	MaxWASMBinaryBytes int64 = 50 * 1024 * 1024
	// MaxWASMOutputBytes caps WASM stdout, stderr, and file-output reads at 2 MB.
	MaxWASMOutputBytes = 2 * 1024 * 1024
	// MaxHTTPGetBytes caps http_get host function responses at 2 MB.
	MaxHTTPGetBytes = 2 * 1024 * 1024
	// maxHTTPRedirects caps redirect chains in httpGetFn.
	maxHTTPRedirects = 3
)

// limitedBuffer captures up to limit bytes in memory without erroring or OOMing on overflow.
type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	if limit <= 0 {
		limit = MaxWASMOutputBytes
	}
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			_, _ = b.buf.Write(p[:remaining])
			b.truncated = true
		} else {
			_, _ = b.buf.Write(p)
		}
	} else if len(p) > 0 {
		b.truncated = true
	}
	return n, nil
}

func (b *limitedBuffer) Bytes() []byte   { return b.buf.Bytes() }
func (b *limitedBuffer) String() string  { return b.buf.String() }
func (b *limitedBuffer) Truncated() bool { return b.truncated }

type cachedModule struct {
	sha256   string
	compiled wazero.CompiledModule
	refs     int
	evicted  bool
}

type networkPolicy struct {
	isolationMode  string
	whitelistedIPs []string
}

type Runner struct {
	Timeout         time.Duration
	MaxBinaryBytes  int64
	runtime         wazero.Runtime
	cache           map[string]*cachedModule // indexed by SHA-256 hex of .wasm bytes
	pathHash        map[string]string        // maps normalized .wasm path -> latest SHA-256 hex
	mu              sync.RWMutex
	isolationMode   string
	whitelistedIPs  []string
	allowHostMounts bool
}

func (rn *Runner) maxBinaryBytes() int64 {
	rn.mu.RLock()
	limit := rn.MaxBinaryBytes
	rn.mu.RUnlock()
	if limit <= 0 {
		return MaxWASMBinaryBytes
	}
	return limit
}

func NewRunner() *Runner {
	ctx := context.Background()

	// Hardening: 128MB Memory Limit (2048 pages * 64KB) and strict context cancellation bounds.
	config := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(2048).
		WithCloseOnContextDone(true)

	r := wazero.NewRuntimeWithConfig(ctx, config)

	wasi_snapshot_preview1.MustInstantiate(ctx, r)
	if err := createHostBuilder(r, ctx); err != nil {
		fmt.Printf("Warning: Failed to initialize Vraxter V1 host functions: %v\n", err)
	}

	rn := &Runner{
		Timeout:         60 * time.Second,
		MaxBinaryBytes:  MaxWASMBinaryBytes,
		runtime:         r,
		cache:           make(map[string]*cachedModule),
		pathHash:        make(map[string]string),
		isolationMode:   "airgap",
		allowHostMounts: false,
	}
	return rn
}

// SetNetworkPolicy updates the isolation mode and IP whitelist enforced by the WASM runner and http_get hostcall.
func (rn *Runner) SetNetworkPolicy(isolationMode string, whitelistedIPs []string) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	if strings.TrimSpace(isolationMode) != "" {
		rn.isolationMode = isolationMode
	}
	rn.whitelistedIPs = make([]string, len(whitelistedIPs))
	copy(rn.whitelistedIPs, whitelistedIPs)
}

// SetAllowHostMounts controls whether explicit source:"host" / legacy host mounts are permitted outside airgap.
// In airgap mode, host mounts are always forbidden regardless of this flag.
func (rn *Runner) SetAllowHostMounts(allow bool) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.allowHostMounts = allow
}

// HasCachedHash reports whether a compiled WASM module with the given SHA-256 is currently cached.
func (rn *Runner) HasCachedHash(sha256Hex string) bool {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	_, ok := rn.cache[sha256Hex]
	return ok
}

// CacheSize returns the number of compiled modules currently held in the runner cache.
func (rn *Runner) CacheSize() int {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return len(rn.cache)
}

// InvalidatePath evicts any cached compiled module associated with the given .wasm path.
func (rn *Runner) InvalidatePath(wasmPath string) {
	normPath := filepath.Clean(wasmPath)
	var toClose wazero.CompiledModule

	rn.mu.Lock()
	if oldHash, ok := rn.pathHash[normPath]; ok {
		delete(rn.pathHash, normPath)
		if !rn.isHashReferencedLocked(oldHash) {
			if oldEntry, exists := rn.cache[oldHash]; exists {
				delete(rn.cache, oldHash)
				oldEntry.evicted = true
				if oldEntry.refs == 0 {
					toClose = oldEntry.compiled
				}
			}
		}
	}
	rn.mu.Unlock()

	if toClose != nil {
		_ = toClose.Close(context.Background())
	}
}

func (rn *Runner) isHashReferencedLocked(hash string) bool {
	for _, h := range rn.pathHash {
		if h == hash {
			return true
		}
	}
	return false
}

func (rn *Runner) releaseModule(entry *cachedModule) {
	if entry == nil {
		return
	}
	var toClose wazero.CompiledModule
	rn.mu.Lock()
	entry.refs--
	if entry.evicted && entry.refs == 0 {
		toClose = entry.compiled
	}
	rn.mu.Unlock()

	if toClose != nil {
		_ = toClose.Close(context.Background())
	}
}

type ctxKey string

const (
	manifestKey      ctxKey = "vrax_manifest"
	networkPolicyKey ctxKey = "vrax_net_policy"
)

func validateHTTPGetTarget(rawURL string, manifest types.SkillManifest, policy networkPolicy) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" || u.Hostname() == "" {
		return fmt.Errorf("invalid URL %q", rawURL)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("disallowed URL scheme %q", u.Scheme)
	}

	// Enforce V1 manifest network constraints if declared
	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil && manifest.ManifestV1.Capabilities.Network != nil {
		netCap := manifest.ManifestV1.Capabilities.Network
		if len(netCap.AllowedSchemes) > 0 {
			schemeOK := false
			for _, allowedScheme := range netCap.AllowedSchemes {
				if strings.EqualFold(strings.TrimSpace(allowedScheme), scheme) {
					schemeOK = true
					break
				}
			}
			if !schemeOK {
				return fmt.Errorf("scheme %q not in manifest allowed_schemes", scheme)
			}
		}
		if len(netCap.AllowedHosts) > 0 {
			hostOK := false
			host := strings.ToLower(u.Hostname())
			for _, allowedHost := range netCap.AllowedHosts {
				ah := strings.ToLower(strings.TrimSpace(allowedHost))
				if host == ah {
					hostOK = true
					break
				}
			}
			if !hostOK {
				return fmt.Errorf("host %q not in manifest allowed_hosts", u.Hostname())
			}
		}
	}

	// Enforce Vraxter network isolation perimeter (airgap / local_lan / hybrid)
	allowed, err := utils.IsAllowedNetworkWithMode(rawURL, policy.isolationMode, policy.whitelistedIPs)
	if err != nil || !allowed {
		return fmt.Errorf("blocked by %s network shield: %v", policy.isolationMode, err)
	}
	return nil
}

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

	if outMax == 0 {
		return 0
	}

	urlBytes, ok := mod.Memory().Read(urlPtr, urlLen)
	if !ok {
		return 0
	}
	rawURL := string(urlBytes)

	policy := networkPolicy{isolationMode: "airgap"}
	if pv, ok := ctx.Value(networkPolicyKey).(networkPolicy); ok && pv.isolationMode != "" {
		policy = pv
	}

	// Enforce Airgap / Isolation Network Shield BEFORE issuing request (zero DNS leaks)
	if err := validateHTTPGetTarget(rawURL, manifest, policy); err != nil {
		fmt.Printf("\n[SHIELD SECURITY ALERT] Skill '%s' outbound HTTP request to '%s' blocked: %v\n", manifest.ID, rawURL, err)
		return 0
	}

	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: utils.NewAirgapTransport(policy.isolationMode, policy.whitelistedIPs),
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxHTTPRedirects {
				return fmt.Errorf("too many redirects (max %d)", maxHTTPRedirects)
			}
			// Re-validate every redirect destination against the network shield and manifest allowlist
			if err := validateHTTPGetTarget(req.URL.String(), manifest, policy); err != nil {
				fmt.Printf("\n[SHIELD SECURITY ALERT] Skill '%s' HTTP redirect to '%s' blocked: %v\n", manifest.ID, req.URL.String(), err)
				return err
			}
			return nil
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	// Bound body read strictly with io.LimitReader (min of caller outMax and 2 MB hard cap)
	readLimit := int64(outMax)
	if readLimit > MaxHTTPGetBytes {
		readLimit = MaxHTTPGetBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, readLimit))
	if err != nil {
		return 0
	}
	if uint32(len(body)) > outMax {
		body = body[:outMax]
	}
	if !mod.Memory().Write(outPtr, body) {
		return 0
	}
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

func hasPrivilegedCapabilities(v1 *types.SkillManifestV1) bool {
	if v1 == nil || v1.Capabilities == nil {
		return false
	}
	caps := v1.Capabilities
	if caps.FS != nil && len(caps.FS.Mounts) > 0 {
		return true
	}
	if caps.Network != nil && caps.Network.Mode != "" && strings.ToLower(caps.Network.Mode) != "none" {
		return true
	}
	if caps.Env != nil && len(caps.Env.Vars) > 0 {
		return true
	}
	return false
}

// containSubpath validates that sub stays strictly within baseDir without escaping via ".." or symlinks.
func containSubpath(baseDir, sub string) (string, error) {
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return "", fmt.Errorf("invalid mount base directory: %w", err)
	}
	baseAbs = filepath.Clean(baseAbs)

	if strings.TrimSpace(sub) == "" {
		return baseAbs, nil
	}
	if strings.ContainsRune(sub, '\x00') {
		return "", fmt.Errorf("mount subpath contains null byte")
	}
	if filepath.IsAbs(sub) || strings.HasPrefix(sub, "/") || strings.HasPrefix(sub, "\\") {
		return "", fmt.Errorf("mount subpath '%s' must be relative", sub)
	}

	normSub := strings.ReplaceAll(sub, "\\", "/")
	for _, seg := range strings.Split(normSub, "/") {
		if seg == ".." {
			return "", fmt.Errorf("mount subpath '%s' attempts path traversal ('..')", sub)
		}
	}

	joined := filepath.Clean(filepath.Join(baseAbs, filepath.FromSlash(normSub)))
	rel, err := filepath.Rel(baseAbs, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("mount subpath '%s' escapes base directory '%s'", sub, baseAbs)
	}

	// If both base and target exist, verify symlinks do not escape baseDir
	if realBase, errBase := filepath.EvalSymlinks(baseAbs); errBase == nil {
		if realJoined, errJoined := filepath.EvalSymlinks(joined); errJoined == nil {
			realRel, errRel := filepath.Rel(realBase, realJoined)
			if errRel != nil || realRel == ".." || strings.HasPrefix(realRel, ".."+string(os.PathSeparator)) || filepath.IsAbs(realRel) {
				return "", fmt.Errorf("mount subpath '%s' escapes base directory via symlink", sub)
			}
		}
	}

	return joined, nil
}

func validateGuestPath(guestPath string) error {
	if !strings.HasPrefix(guestPath, "/") {
		return fmt.Errorf("guest path '%s' must start with '/'", guestPath)
	}
	if strings.ContainsRune(guestPath, '\x00') || strings.ContainsRune(guestPath, '\\') {
		return fmt.Errorf("guest path '%s' contains forbidden characters", guestPath)
	}
	for _, seg := range strings.Split(guestPath, "/") {
		if seg == ".." {
			return fmt.Errorf("guest path '%s' contains path traversal ('..')", guestPath)
		}
	}
	return nil
}

func (rn *Runner) getOrCompileModule(ctx context.Context, wasmPath, expectedHash string) (*cachedModule, error) {
	normPath := filepath.Clean(wasmPath)

	// Fast path: check SHA-256 index in cache
	rn.mu.Lock()
	if entry, ok := rn.cache[expectedHash]; ok && !entry.evicted {
		entry.refs++
		rn.pathHash[normPath] = expectedHash
		rn.mu.Unlock()
		return entry, nil
	}
	rn.mu.Unlock()

	// Cache miss: enforce binary size limit before loading bytes into memory,
	// and verify SHA-256 directly over the read slice (eliminates hash/compile TOCTOU)
	maxBytes := rn.maxBinaryBytes()
	f, err := os.Open(wasmPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open WASM isolation binary %s: %w", wasmPath, err)
	}
	fi, statErr := f.Stat()
	if statErr != nil {
		_ = f.Close()
		return nil, fmt.Errorf("failed to stat WASM isolation binary %s: %w", wasmPath, statErr)
	}
	if fi.Size() > maxBytes {
		_ = f.Close()
		return nil, fmt.Errorf("VRAXTER SHIELD: WASM binary %s exceeds maximum allowed size (%d > %d bytes)", wasmPath, fi.Size(), maxBytes)
	}
	wasmBytes, err := io.ReadAll(io.LimitReader(f, maxBytes+1))
	_ = f.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to read WASM isolation binary %s: %w", wasmPath, err)
	}
	if int64(len(wasmBytes)) > maxBytes {
		return nil, fmt.Errorf("VRAXTER SHIELD: WASM binary %s exceeds maximum allowed size (%d bytes)", wasmPath, maxBytes)
	}
	sum := sha256.Sum256(wasmBytes)
	actualHash := hex.EncodeToString(sum[:])
	if actualHash != expectedHash {
		return nil, fmt.Errorf("WASM binary changed concurrently on disk (expected %s, got %s)", expectedHash, actualHash)
	}

	compiled, err := rn.runtime.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile WASM module: %w", err)
	}

	var toCloseOld wazero.CompiledModule
	var toCloseDup wazero.CompiledModule

	rn.mu.Lock()
	var entry *cachedModule
	if existing, exists := rn.cache[actualHash]; exists && !existing.evicted {
		existing.refs++
		entry = existing
		toCloseDup = compiled
	} else {
		entry = &cachedModule{
			sha256:   actualHash,
			compiled: compiled,
			refs:     1,
		}
		rn.cache[actualHash] = entry
	}

	// Evict obsolete compiled module previously bound to this file path
	if oldHash, hadOld := rn.pathHash[normPath]; hadOld && oldHash != actualHash {
		rn.pathHash[normPath] = actualHash
		if !rn.isHashReferencedLocked(oldHash) {
			if oldEntry, exists := rn.cache[oldHash]; exists {
				delete(rn.cache, oldHash)
				oldEntry.evicted = true
				if oldEntry.refs == 0 {
					toCloseOld = oldEntry.compiled
				}
			}
		}
	} else {
		rn.pathHash[normPath] = actualHash
	}
	rn.mu.Unlock()

	if toCloseDup != nil {
		_ = toCloseDup.Close(context.Background())
	}
	if toCloseOld != nil {
		_ = toCloseOld.Close(context.Background())
	}

	return entry, nil
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

	rn.mu.RLock()
	isoMode := rn.isolationMode
	wlCopy := make([]string, len(rn.whitelistedIPs))
	copy(wlCopy, rn.whitelistedIPs)
	allowHostMounts := rn.allowHostMounts
	rn.mu.RUnlock()

	isAirgap := isoMode == "airgap" || isoMode == "strict_local"

	timeout := manifest.GetExecutionTimeout(rn.Timeout)
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	execCtx = context.WithValue(execCtx, manifestKey, manifest)
	execCtx = context.WithValue(execCtx, networkPolicyKey, networkPolicy{
		isolationMode:  isoMode,
		whitelistedIPs: wlCopy,
	})

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
	isSandboxSafe := len(manifest.Permissions) == 0 && !hasPrivilegedCapabilities(manifest.ManifestV1)

	isAuthorized := isOfficial || hasHighReputation || isSandboxSafe

	// Fail-closed: unset Tier (0) or Tier3Unverified require explicit trust for any privileged skill
	if !isAuthorized && (manifest.Tier == 0 || manifest.Tier >= types.Tier3Unverified) {
		return nil, fmt.Errorf(
			"VRAXTER SHIELD: Skill '%s' requires specialized permissions and lacks community trust. "+
				"To allow this skill, run 'vraxter skills trust %s'", manifest.ID, manifest.ID)
	}

	// Validate mount policy and path containment BEFORE compiling/instantiating
	type activeMount struct {
		guest string
		host  string
		mode  string
	}
	var activeMounts []activeMount

	runTempDir, err := os.MkdirTemp("", "vraxter-run-"+manifest.ID+"-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create ephemeral skill sandbox directory: %w", err)
	}
	defer os.RemoveAll(runTempDir)

	fsConfig := wazero.NewFSConfig()

	if manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil && manifest.ManifestV1.Capabilities.FS != nil && len(manifest.ManifestV1.Capabilities.FS.Mounts) > 0 {
		for _, mount := range manifest.ManifestV1.Capabilities.FS.Mounts {
			if err := validateGuestPath(mount.Guest); err != nil {
				return nil, fmt.Errorf("shield: invalid mount guest path: %w", err)
			}

			var hostBase string
			switch strings.ToLower(mount.Source) {
			case "workspace":
				hostBase, err = GetWorkspaceRoot()
				if err != nil || hostBase == "" {
					hostBase, _ = os.Getwd()
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
				if isAirgap {
					return nil, fmt.Errorf("VRAXTER SHIELD: mount source 'host' is strictly forbidden in %s mode", isoMode)
				}
				if !allowHostMounts {
					return nil, fmt.Errorf("VRAXTER SHIELD: mount source 'host' is disabled by default security policy")
				}
				hostBase = ResolveMountPath(mount.HostPath)
			default:
				return nil, fmt.Errorf("VRAXTER SHIELD: unknown mount source '%s'", mount.Source)
			}

			hostDir, err := containSubpath(hostBase, mount.Subpath)
			if err != nil {
				return nil, fmt.Errorf("VRAXTER SHIELD: %w", err)
			}
			if err := os.MkdirAll(hostDir, 0700); err != nil {
				return nil, fmt.Errorf("failed to prepare mount directory: %w", err)
			}

			if strings.ToLower(mount.Mode) == "ro" {
				fsConfig = fsConfig.WithReadOnlyDirMount(hostDir, mount.Guest)
			} else {
				fsConfig = fsConfig.WithDirMount(hostDir, mount.Guest)
			}

			activeMounts = append(activeMounts, activeMount{
				guest: path.Clean(mount.Guest),
				host:  hostDir,
				mode:  mount.Mode,
			})
		}
	} else {
		// Fallback for legacy permission strings (blocked in airgap and by default)
		for _, perm := range manifest.Permissions {
			p := strings.TrimSpace(perm)
			low := strings.ToLower(p)
			isRead := strings.HasPrefix(low, "fs_read:") || strings.HasPrefix(low, "fs:read:") || strings.HasPrefix(low, "filesystem:read:")
			isWrite := strings.HasPrefix(low, "fs_write:") || strings.HasPrefix(low, "fs:write:") || strings.HasPrefix(low, "filesystem:write:")
			if isRead || isWrite {
				if isAirgap || !allowHostMounts {
					return nil, fmt.Errorf("VRAXTER SHIELD: legacy host filesystem permission '%s' is blocked by default / in airgap mode", p)
				}
				idx := strings.Index(p, ":")
				if strings.HasPrefix(low, "fs:read:") || strings.HasPrefix(low, "filesystem:read:") || strings.HasPrefix(low, "fs:write:") || strings.HasPrefix(low, "filesystem:write:") {
					idx = strings.LastIndex(p, ":")
				}
				dir := strings.TrimSpace(p[idx+1:])
				resolvedDir := ResolveMountPath(dir)
				if resolvedDir != "" {
					if isRead {
						fsConfig = fsConfig.WithReadOnlyDirMount(resolvedDir, resolvedDir)
						activeMounts = append(activeMounts, activeMount{guest: resolvedDir, host: resolvedDir, mode: "ro"})
					} else {
						fsConfig = fsConfig.WithDirMount(resolvedDir, resolvedDir)
						activeMounts = append(activeMounts, activeMount{guest: resolvedDir, host: resolvedDir, mode: "rw"})
					}
				}
			}
		}
	}

	// Validate io.output.path statically BEFORE instantiating
	if manifest.ManifestV1 != nil && manifest.ManifestV1.IO != nil && strings.ToLower(manifest.ManifestV1.IO.Output.Via) == "file" {
		if err := validateGuestPath(manifest.ManifestV1.IO.Output.Path); err != nil {
			return nil, fmt.Errorf("VRAXTER SHIELD: invalid io.output.path: %w", err)
		}
	}

	// Fetch or compile WASM module indexed by SHA-256 (evicting stale modules on hash change)
	cached, err := rn.getOrCompileModule(execCtx, manifest.Command, currentHash)
	if err != nil {
		return nil, err
	}
	defer rn.releaseModule(cached)

	stdout := newLimitedBuffer(MaxWASMOutputBytes)
	stderr := newLimitedBuffer(MaxWASMOutputBytes)
	config := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(inputBytes)).
		WithStdout(stdout).
		WithStderr(stderr).
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

	mod, err := rn.runtime.InstantiateModule(execCtx, cached.compiled, config)
	if err != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return nil, fmt.Errorf("wasm skill execution timed out")
		}
		return nil, fmt.Errorf(
			"wasm skill crashed during execution: %v, stderr: %s",
			err,
			stderr.String(),
		)
	}
	defer mod.Close(execCtx)

	// Check if output is via file (confined strictly inside the matched mount using os.OpenRoot)
	if manifest.ManifestV1 != nil && manifest.ManifestV1.IO != nil && strings.ToLower(manifest.ManifestV1.IO.Output.Via) == "file" {
		outGuestPath := manifest.ManifestV1.IO.Output.Path
		if err := validateGuestPath(outGuestPath); err != nil {
			return nil, fmt.Errorf("VRAXTER SHIELD: invalid io.output.path: %w", err)
		}
		cleanGuestOut := path.Clean(outGuestPath)

		var matchedMount *activeMount
		longestPrefix := -1
		for i := range activeMounts {
			m := &activeMounts[i]
			gPrefix := m.guest
			if cleanGuestOut == gPrefix || strings.HasPrefix(cleanGuestOut, strings.TrimSuffix(gPrefix, "/")+"/") {
				if len(gPrefix) > longestPrefix {
					longestPrefix = len(gPrefix)
					matchedMount = m
				}
			}
		}

		if matchedMount == nil {
			return nil, fmt.Errorf("skill '%s' was configured with file output at '%s', but no mount matches that guest path", manifest.ID, outGuestPath)
		}

		relPath := strings.TrimPrefix(cleanGuestOut, strings.TrimSuffix(matchedMount.guest, "/"))
		relPath = strings.TrimPrefix(relPath, "/")
		if relPath == "" || relPath == "." || relPath == ".." || strings.HasPrefix(relPath, "../") {
			return nil, fmt.Errorf("VRAXTER SHIELD: io.output.path '%s' does not resolve to a valid file inside mount '%s'", outGuestPath, matchedMount.guest)
		}

		mountRoot, err := os.OpenRoot(matchedMount.host)
		if err != nil {
			return nil, fmt.Errorf("failed to open output mount root: %w", err)
		}
		defer mountRoot.Close()

		outFile, err := mountRoot.Open(relPath)
		if err != nil {
			return nil, fmt.Errorf("skill '%s' finished execution but failed to read expected output file at guest '%s': %w", manifest.ID, outGuestPath, err)
		}
		defer outFile.Close()

		data, err := io.ReadAll(io.LimitReader(outFile, MaxWASMOutputBytes))
		if err != nil {
			return nil, fmt.Errorf("skill '%s' failed reading output file '%s': %w", manifest.ID, outGuestPath, err)
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
	maxBytes := rn.maxBinaryBytes()
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if fi.Size() > maxBytes {
		return "", fmt.Errorf("WASM binary %s exceeds maximum allowed size (%d > %d bytes)", path, fi.Size(), maxBytes)
	}

	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, maxBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxBytes {
		return "", fmt.Errorf("WASM binary %s exceeds maximum allowed size (%d bytes)", path, maxBytes)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
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
