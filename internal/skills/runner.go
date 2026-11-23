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
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/experimental/sysfs"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/telemetry"
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

	// DefaultMemoryLimitPages is the default per-instance WASM memory ceiling (128 MB = 2048 * 64KB pages).
	DefaultMemoryLimitPages uint32 = 2048
	// MinMemoryLimitPages is the minimum configurable per-instance WASM memory limit (1 MB = 16 * 64KB pages).
	MinMemoryLimitPages uint32 = 16
	// DefaultMaxConcurrentExecs bounds simultaneous WASM module instantiations to prevent host OOM/CPU spikes.
	DefaultMaxConcurrentExecs = 6
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

type moduleCacheKey struct {
	sha256      string
	memoryPages uint32
}

type cachedModule struct {
	key      moduleCacheKey
	runtime  wazero.Runtime
	compiled wazero.CompiledModule
	refs     int
	evicted  bool
}

type networkPolicy struct {
	isolationMode  string
	whitelistedIPs []string
}

type Runner struct {
	Timeout             time.Duration
	MaxBinaryBytes      int64
	MaxMemoryPages      uint32
	compilationCache    wazero.CompilationCache
	runtimes            map[uint32]wazero.Runtime       // keyed by memoryLimitPages
	cache               map[moduleCacheKey]*cachedModule // indexed by (SHA-256, memoryLimitPages)
	pathHash            map[string]string                // maps normalized .wasm path -> latest SHA-256 hex
	execSem             chan struct{}                    // bounded execution concurrency semaphore
	mu                  sync.RWMutex
	isolationMode       string
	whitelistedIPs      []string
	allowHostMounts     bool
	trustedSignersDir   string
	requireSignedSkills bool
	auditLedger         *audit.Ledger
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

func (rn *Runner) effectiveMemoryLimitPages(manifest types.SkillManifest) uint32 {
	rn.mu.RLock()
	maxCeiling := rn.MaxMemoryPages
	rn.mu.RUnlock()
	if maxCeiling == 0 {
		maxCeiling = DefaultMemoryLimitPages
	}

	pages := manifest.GetMemoryLimitPages(maxCeiling)
	if pages < MinMemoryLimitPages {
		pages = MinMemoryLimitPages
	}
	if pages > maxCeiling {
		pages = maxCeiling
	}
	return pages
}

func NewRunner() *Runner {
	rn := &Runner{
		Timeout:          60 * time.Second,
		MaxBinaryBytes:   MaxWASMBinaryBytes,
		MaxMemoryPages:   DefaultMemoryLimitPages,
		compilationCache: wazero.NewCompilationCache(),
		runtimes:         make(map[uint32]wazero.Runtime),
		cache:            make(map[moduleCacheKey]*cachedModule),
		pathHash:         make(map[string]string),
		execSem:          make(chan struct{}, DefaultMaxConcurrentExecs),
		isolationMode:    "airgap",
		allowHostMounts:  false,
	}

	// Pre-initialize the default 128 MB runtime eagerly.
	if _, err := rn.getRuntimeForPages(DefaultMemoryLimitPages); err != nil {
		fmt.Printf("Warning: Failed to initialize default Wazero runtime: %v\n", err)
	}
	return rn
}

func (rn *Runner) getRuntimeForPages(pages uint32) (wazero.Runtime, error) {
	rn.mu.RLock()
	rt, ok := rn.runtimes[pages]
	rn.mu.RUnlock()
	if ok {
		return rt, nil
	}

	rn.mu.Lock()
	defer rn.mu.Unlock()
	if rt, ok := rn.runtimes[pages]; ok {
		return rt, nil
	}

	ctx := context.Background()
	cfg := wazero.NewRuntimeConfig().
		WithMemoryLimitPages(pages).
		WithCloseOnContextDone(true).
		WithCompilationCache(rn.compilationCache)

	newRT := wazero.NewRuntimeWithConfig(ctx, cfg)
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, newRT); err != nil {
		_ = newRT.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate WASI preview1: %w", err)
	}
	if err := createHostBuilder(newRT, ctx); err != nil {
		_ = newRT.Close(ctx)
		return nil, fmt.Errorf("failed to instantiate Vraxter host module: %w", err)
	}

	rn.runtimes[pages] = newRT
	return newRT, nil
}

// SetMaxConcurrentExecs configures the maximum number of simultaneous WASM executions.
// Values <= 0 reset to DefaultMaxConcurrentExecs.
func (rn *Runner) SetMaxConcurrentExecs(limit int) {
	if limit <= 0 {
		limit = DefaultMaxConcurrentExecs
	}
	rn.mu.Lock()
	rn.execSem = make(chan struct{}, limit)
	rn.mu.Unlock()
}

// MaxConcurrentExecs returns the configured concurrency capacity of the execution semaphore.
func (rn *Runner) MaxConcurrentExecs() int {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return cap(rn.execSem)
}

func (rn *Runner) acquireExecSlot(ctx context.Context) (func(), error) {
	rn.mu.RLock()
	sem := rn.execSem
	rn.mu.RUnlock()
	if sem == nil {
		return func() {}, nil
	}

	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("VRAXTER SHIELD: WASM execution concurrency limit (%d) reached: %w", cap(sem), ctx.Err())
	}
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

// SetTrustedSignersDir configures the directory containing trusted Ed25519 public keys for pre-execution verification.
func (rn *Runner) SetTrustedSignersDir(dir string) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.trustedSignersDir = strings.TrimSpace(dir)
}

// TrustedSignersDir returns the configured directory of trusted Ed25519 public keys.
func (rn *Runner) TrustedSignersDir() string {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return rn.trustedSignersDir
}

// SetRequireSignedSkills controls whether unsigned skills (missing a valid Ed25519 .sig) are blocked at runtime.
func (rn *Runner) SetRequireSignedSkills(require bool) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.requireSignedSkills = require
}

// RequireSignedSkills reports whether unsigned skills are blocked at runtime.
func (rn *Runner) RequireSignedSkills() bool {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return rn.requireSignedSkills
}

// SetAuditLedger attaches a forensic audit ledger to the WASM runner.
func (rn *Runner) SetAuditLedger(l *audit.Ledger) {
	rn.mu.Lock()
	defer rn.mu.Unlock()
	rn.auditLedger = l
}

func (rn *Runner) recordAudit(ctx context.Context, eventType string, payload interface{}) {
	rn.mu.RLock()
	l := rn.auditLedger
	rn.mu.RUnlock()
	if l == nil {
		l = audit.DefaultLedger()
	}
	if l == nil {
		return
	}
	_, _ = l.Append(ctx, audit.AuditEventInput{
		EventType: eventType,
		Payload:   payload,
	})
}

func (rn *Runner) verifyPreExecutionSignature(manifest *types.SkillManifest, currentHash string) (string, error) {
	sigPath := security.DiscoverSkillSignaturePath(manifest.Command, "", manifest.ID)
	if sigPath == "" {
		if rn.RequireSignedSkills() {
			return "", fmt.Errorf("VRAXTER SHIELD: skill '%s' is unsigned (.sig missing) and require_signed_skills is enabled (Tier3Unverified blocked)", manifest.ID)
		}
		return "", nil
	}

	env, err := security.LoadSignatureEnvelope(sigPath)
	if err != nil {
		return "", fmt.Errorf("VRAXTER SHIELD: skill '%s' has a corrupt signature file: %w", manifest.ID, err)
	}
	if !strings.EqualFold(strings.TrimSpace(env.WASMSha256), strings.TrimSpace(currentHash)) {
		return "", fmt.Errorf("VRAXTER SHIELD: skill '%s' WASM binary SHA-256 (%s) does not match Ed25519 signature (%s)", manifest.ID, currentHash, env.WASMSha256)
	}

	trustDir := rn.TrustedSignersDir()
	if trustDir == "" && strings.TrimSpace(manifest.Command) != "" {
		trustDir = filepath.Join(filepath.Dir(filepath.Dir(filepath.Clean(manifest.Command))), "trusted_signers")
	}
	store := security.NewTrustedSignerStore(trustDir)

	var verifiedSigner *security.TrustedSigner
	checkedManifest := false

	// 1. If an adjacent <id>.manifest.json or manifest.json exists on disk, verify it matches the signature.
	if strings.TrimSpace(manifest.Command) != "" {
		wasmDir := filepath.Dir(manifest.Command)
		baseNoExt := strings.TrimSuffix(filepath.Base(manifest.Command), filepath.Ext(manifest.Command))
		for _, cand := range []string{
			filepath.Join(wasmDir, manifest.ID+".manifest.json"),
			filepath.Join(wasmDir, baseNoExt+".manifest.json"),
			filepath.Join(wasmDir, "manifest.json"),
		} {
			if raw, readErr := os.ReadFile(cand); readErr == nil {
				canonicalDisk, cErr := security.CanonicalizeManifestJSON(raw)
				if cErr != nil {
					return "", fmt.Errorf("VRAXTER SHIELD: failed canonicalizing on-disk manifest %s: %w", cand, cErr)
				}
				signer, _, vErr := store.VerifySkillAgainstStore(manifest.Command, canonicalDisk, sigPath)
				if vErr != nil {
					return "", vErr
				}
				verifiedSigner = signer
				checkedManifest = true
				break
			}
		}
	}

	// 2. If manifest.ManifestV1 is populated in memory, also verify it matches the signature (prevents in-memory tampering).
	if manifest.ManifestV1 != nil {
		canonicalMem, cErr := security.CanonicalizeSkillManifestV1(manifest.ManifestV1)
		if cErr != nil {
			return "", fmt.Errorf("VRAXTER SHIELD: failed canonicalizing in-memory ManifestV1: %w", cErr)
		}
		signer, _, vErr := store.VerifySkillAgainstStore(manifest.Command, canonicalMem, sigPath)
		if vErr != nil {
			return "", vErr
		}
		verifiedSigner = signer
		checkedManifest = true
	}

	if !checkedManifest || verifiedSigner == nil {
		return "", fmt.Errorf("VRAXTER SHIELD: signed skill '%s' is missing a verifiable V1 manifest", manifest.ID)
	}

	manifest.Tier = verifiedSigner.TrustTier
	manifest.IsOfficial = (verifiedSigner.TrustTier == types.Tier1Official)
	return verifiedSigner.KeyID, nil
}

// HasCachedHash reports whether a compiled WASM module with the given SHA-256 is currently cached.
func (rn *Runner) HasCachedHash(sha256Hex string) bool {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	for k := range rn.cache {
		if k.sha256 == sha256Hex {
			return true
		}
	}
	return false
}

// CacheSize returns the number of compiled modules currently held in the runner cache.
func (rn *Runner) CacheSize() int {
	rn.mu.RLock()
	defer rn.mu.RUnlock()
	return len(rn.cache)
}

func (rn *Runner) evictHashLocked(hash string) []wazero.CompiledModule {
	var toClose []wazero.CompiledModule
	for k, entry := range rn.cache {
		if k.sha256 != hash {
			continue
		}
		delete(rn.cache, k)
		entry.evicted = true
		if entry.refs == 0 {
			toClose = append(toClose, entry.compiled)
		}
	}
	return toClose
}

// InvalidatePath evicts any cached compiled module associated with the given .wasm path.
func (rn *Runner) InvalidatePath(wasmPath string) {
	normPath := filepath.Clean(wasmPath)
	var toClose []wazero.CompiledModule

	rn.mu.Lock()
	if oldHash, ok := rn.pathHash[normPath]; ok {
		delete(rn.pathHash, normPath)
		if !rn.isHashReferencedLocked(oldHash) {
			toClose = rn.evictHashLocked(oldHash)
		}
	}
	rn.mu.Unlock()

	for _, c := range toClose {
		_ = c.Close(context.Background())
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

func createHostBuilder(rt wazero.Runtime, ctx context.Context) error {
	_, err := rt.NewHostModuleBuilder("vrax_sandbox").
		NewFunctionBuilder().
		WithFunc(httpGetFn).
		Export("http_get").
		Instantiate(ctx)

	return err
}

func (rn *Runner) Close(ctx context.Context) error {
	rn.mu.Lock()
	rts := make([]wazero.Runtime, 0, len(rn.runtimes))
	for _, rt := range rn.runtimes {
		rts = append(rts, rt)
	}
	rn.runtimes = make(map[uint32]wazero.Runtime)
	rn.cache = make(map[moduleCacheKey]*cachedModule)
	rn.pathHash = make(map[string]string)
	cc := rn.compilationCache
	rn.mu.Unlock()

	var firstErr error
	for _, rt := range rts {
		if err := rt.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if cc != nil {
		if err := cc.Close(ctx); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (rn *Runner) Execute(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}) (*types.ExecutionResult, error) {
	if manifest.Engine != "wasm" && manifest.Engine != "" {
		return nil, fmt.Errorf("skill engine '%s' is not supported (only wasm is allowed)", manifest.Engine)
	}
	return rn.executeWasmInternal(ctx, manifest, params, false)
}

// ExecuteDryRun validates a freshly compiled skill's WASM imports against its real manifest
// and smoke-tests it inside a zero-mount, zero-network sandbox with "_vraxter_dry_run": true.
func (rn *Runner) ExecuteDryRun(ctx context.Context, manifest types.SkillManifest) (*types.ExecutionResult, error) {
	if manifest.Engine != "wasm" && manifest.Engine != "" {
		return nil, fmt.Errorf("skill engine '%s' is not supported (only wasm is allowed)", manifest.Engine)
	}
	return rn.executeWasmInternal(ctx, manifest, map[string]interface{}{"_vraxter_dry_run": true}, true)
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

func (rn *Runner) getOrCompileModule(ctx context.Context, wasmPath, expectedHash string, memPages uint32) (*cachedModule, error) {
	normPath := filepath.Clean(wasmPath)
	key := moduleCacheKey{sha256: expectedHash, memoryPages: memPages}

	// Fast path: check (SHA-256, memoryPages) index in cache
	rn.mu.Lock()
	if entry, ok := rn.cache[key]; ok && !entry.evicted {
		entry.refs++
		rn.pathHash[normPath] = expectedHash
		rn.mu.Unlock()
		return entry, nil
	}
	rn.mu.Unlock()

	rt, err := rn.getRuntimeForPages(memPages)
	if err != nil {
		return nil, err
	}

	// Cache miss: enforce binary size limit before loading bytes into memory,
	// and verify SHA-256 directly over the read slice (eliminates hash/compile TOCTOU).
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

	compiled, err := rt.CompileModule(ctx, wasmBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to compile WASM module: %w", err)
	}

	var toCloseOld []wazero.CompiledModule
	var toCloseDup wazero.CompiledModule

	rn.mu.Lock()
	var entry *cachedModule
	actualKey := moduleCacheKey{sha256: actualHash, memoryPages: memPages}
	if existing, exists := rn.cache[actualKey]; exists && !existing.evicted {
		existing.refs++
		entry = existing
		toCloseDup = compiled
	} else {
		entry = &cachedModule{
			key:      actualKey,
			runtime:  rt,
			compiled: compiled,
			refs:     1,
		}
		rn.cache[actualKey] = entry
	}

	// Evict obsolete compiled modules previously bound to this file path
	if oldHash, hadOld := rn.pathHash[normPath]; hadOld && oldHash != actualHash {
		rn.pathHash[normPath] = actualHash
		if !rn.isHashReferencedLocked(oldHash) {
			toCloseOld = rn.evictHashLocked(oldHash)
		}
	} else {
		rn.pathHash[normPath] = actualHash
	}
	rn.mu.Unlock()

	if toCloseDup != nil {
		_ = toCloseDup.Close(context.Background())
	}
	for _, old := range toCloseOld {
		_ = old.Close(context.Background())
	}

	return entry, nil
}

func (rn *Runner) executeWasmInternal(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}, isDryRun bool) (*types.ExecutionResult, error) {
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
	if !isDryRun {
		newCtx, span := telemetry.StartSpan(execCtx, "runner.ExecuteWASM")
		execCtx = newCtx
		defer span.End()
	}
	execCtx = context.WithValue(execCtx, manifestKey, manifest)
	execCtx = context.WithValue(execCtx, networkPolicyKey, networkPolicy{
		isolationMode:  isoMode,
		whitelistedIPs: wlCopy,
	})
	if isDryRun {
		execCtx = context.WithValue(execCtx, dryRunCtxKey, true)
	}

	// Acquire bounded execution slot (context-aware semaphore prevents concurrent OOM/CPU floods).
	releaseSlot, err := rn.acquireExecSlot(execCtx)
	if err != nil {
		return nil, err
	}
	defer releaseSlot()

	currentHash, err := rn.calculateHash(manifest.Command)
	if err != nil {
		return nil, fmt.Errorf("shield: failed to verify binary integrity: %w", err)
	}
	if manifest.Checksum != "" && currentHash != manifest.Checksum {
		if !isDryRun {
			rn.recordAudit(execCtx, audit.EventPolicyEvaluated, map[string]interface{}{
				"check":       "wasm_binary_integrity",
				"skill_id":    manifest.ID,
				"wasm_sha256": currentHash,
				"decision":    "DENY",
				"reason":      "checksum_mismatch",
			})
		}
		return nil, fmt.Errorf(
			"CRITICAL SECURITY ALERT: Binary %s integrity verification FAILED. "+
				"Expected %s, got %s. Possible hijacking or corruption detected",
			manifest.ID,
			manifest.Checksum,
			currentHash,
		)
	}

	var signerKeyID string
	if !isDryRun {
		var sigErr error
		signerKeyID, sigErr = rn.verifyPreExecutionSignature(&manifest, currentHash)
		if sigErr != nil {
			rn.recordAudit(execCtx, audit.EventPolicyEvaluated, map[string]interface{}{
				"check":       "wasm_signature_verification",
				"skill_id":    manifest.ID,
				"wasm_sha256": currentHash,
				"trust_tier":  int(manifest.Tier),
				"decision":    "DENY",
				"reason":      sigErr.Error(),
			})
			return nil, sigErr
		}

		isOfficial := manifest.IsOfficial
		hasHighReputation := manifest.Score >= 4.0 && manifest.Downloads > 100
		isSandboxSafe := len(manifest.Permissions) == 0 && !hasPrivilegedCapabilities(manifest.ManifestV1)

		isAuthorized := isOfficial || hasHighReputation || isSandboxSafe || manifest.Tier == types.Tier1Official || manifest.Tier == types.Tier2CommunityVerified

		// Fail-closed: unset Tier (0) or Tier3Unverified require explicit trust for any privileged skill.
		if !isAuthorized && (manifest.Tier == 0 || manifest.Tier >= types.Tier3Unverified) {
			rn.recordAudit(execCtx, audit.EventPolicyEvaluated, map[string]interface{}{
				"check":       "wasm_trust_tier",
				"skill_id":    manifest.ID,
				"wasm_sha256": currentHash,
				"trust_tier":  int(manifest.Tier),
				"decision":    "DENY",
				"reason":      "untrusted_privileged_skill",
			})
			return nil, fmt.Errorf(
				"VRAXTER SHIELD: Skill '%s' requires specialized permissions and lacks community trust. "+
					"To allow this skill, run 'vraxter skills trust %s'", manifest.ID, manifest.ID)
		}

		rn.recordAudit(execCtx, audit.EventPolicyEvaluated, map[string]interface{}{
			"check":         "wasm_trust_and_signature",
			"skill_id":      manifest.ID,
			"wasm_sha256":   currentHash,
			"trust_tier":    int(manifest.Tier),
			"signer_key_id": signerKeyID,
			"decision":      "ALLOW",
		})
	}

	// Validate mount policy, path containment, and wrap every mount with quotaFS BEFORE instantiating.
	type activeMount struct {
		guest string
		host  string
		mode  string
	}
	var activeMounts []activeMount
	var quotaMounts []*quotaFS

	runTempDir, err := os.MkdirTemp("", "vraxter-run-"+manifest.ID+"-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create ephemeral skill sandbox directory: %w", err)
	}
	defer os.RemoveAll(runTempDir)

	fsConfig := wazero.NewFSConfig()

	if !isDryRun {
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
					if outDir, ok := params["_output_dir"].(string); ok && outDir != "" {
						hostBase = outDir
					} else {
						userHome, _ := os.UserHomeDir()
						hostBase = filepath.Join(userHome, ".vraxter", "artifacts", "skills", manifest.ID)
					}
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

				qfs := newQuotaFS(hostDir, mount)
				quotaMounts = append(quotaMounts, qfs)
				if sfc, ok := fsConfig.(sysfs.FSConfig); ok {
					fsConfig = sfc.WithSysFSMount(qfs, mount.Guest)
				} else if strings.EqualFold(mount.Mode, "ro") {
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
						mode := "rw"
						if isRead {
							mode = "ro"
						}
						qfs := newQuotaFS(resolvedDir, types.MountSpec{
							Guest:       resolvedDir,
							Source:      "host",
							HostPath:    resolvedDir,
							Mode:        mode,
							AllowDelete: isWrite,
						})
						quotaMounts = append(quotaMounts, qfs)
						if sfc, ok := fsConfig.(sysfs.FSConfig); ok {
							fsConfig = sfc.WithSysFSMount(qfs, resolvedDir)
						} else if isRead {
							fsConfig = fsConfig.WithReadOnlyDirMount(resolvedDir, resolvedDir)
						} else {
							fsConfig = fsConfig.WithDirMount(resolvedDir, resolvedDir)
						}
						activeMounts = append(activeMounts, activeMount{guest: resolvedDir, host: resolvedDir, mode: mode})
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
	}

	// Compute per-instance memory limit pages (clamped to Runner ceiling) and fetch/compile module.
	memPages := rn.effectiveMemoryLimitPages(manifest)
	cached, err := rn.getOrCompileModule(execCtx, manifest.Command, currentHash, memPages)
	if err != nil {
		return nil, err
	}
	defer rn.releaseModule(cached)

	// Static WASM bytecode import verification (Zero Over-Privilege) BEFORE instantiation.
	if err := VerifyCompiledModule(cached.compiled, manifest); err != nil {
		return nil, err
	}

	stdout := newLimitedBuffer(MaxWASMOutputBytes)
	stderr := newLimitedBuffer(MaxWASMOutputBytes)
	config := wazero.NewModuleConfig().
		WithStdin(bytes.NewReader(inputBytes)).
		WithStdout(stdout).
		WithStderr(stderr).
		WithEnv("PATH", "").
		WithFSConfig(fsConfig)

	if !isDryRun && manifest.ManifestV1 != nil && manifest.ManifestV1.Capabilities != nil {
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

	wasmStart := time.Now()
	if !isDryRun {
		rn.recordAudit(execCtx, audit.EventWASMExecStart, map[string]interface{}{
			"skill_id":      manifest.ID,
			"wasm_sha256":   currentHash,
			"trust_tier":    int(manifest.Tier),
			"signer_key_id": signerKeyID,
		})
	}

	mod, err := cached.runtime.InstantiateModule(execCtx, cached.compiled, config)
	if err != nil {
		for _, qm := range quotaMounts {
			if v := qm.Violation(); v != "" {
				if !isDryRun {
					rn.recordAudit(execCtx, audit.EventWASMExecEnd, map[string]interface{}{
						"skill_id":      manifest.ID,
						"wasm_sha256":   currentHash,
						"trust_tier":    int(manifest.Tier),
						"signer_key_id": signerKeyID,
						"exit_status":   "policy_violation",
						"duration_ms":   time.Since(wasmStart).Milliseconds(),
					})
				}
				return nil, fmt.Errorf("VRAXTER SHIELD: filesystem policy violation on mount '%s': %s (guest err: %v)", qm.guestPath, v, err)
			}
		}
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			if !isDryRun {
				rn.recordAudit(execCtx, audit.EventWASMExecEnd, map[string]interface{}{
					"skill_id":      manifest.ID,
					"wasm_sha256":   currentHash,
					"trust_tier":    int(manifest.Tier),
					"signer_key_id": signerKeyID,
					"exit_status":   "timeout",
					"duration_ms":   time.Since(wasmStart).Milliseconds(),
				})
			}
			return nil, fmt.Errorf("wasm skill execution timed out")
		}
		if !isDryRun {
			rn.recordAudit(execCtx, audit.EventWASMExecEnd, map[string]interface{}{
				"skill_id":      manifest.ID,
				"wasm_sha256":   currentHash,
				"trust_tier":    int(manifest.Tier),
				"signer_key_id": signerKeyID,
				"exit_status":   "crashed",
				"duration_ms":   time.Since(wasmStart).Milliseconds(),
			})
		}
		return nil, fmt.Errorf(
			"wasm skill crashed during execution: %v, stderr: %s",
			err,
			stderr.String(),
		)
	}
	_ = mod.Close(execCtx)

	// Surface any quota/policy violation even if guest swallowed the WASI EPERM/EACCES error.
	for _, qm := range quotaMounts {
		if v := qm.Violation(); v != "" {
			if !isDryRun {
				rn.recordAudit(execCtx, audit.EventWASMExecEnd, map[string]interface{}{
					"skill_id":      manifest.ID,
					"wasm_sha256":   currentHash,
					"trust_tier":    int(manifest.Tier),
					"signer_key_id": signerKeyID,
					"exit_status":   "policy_violation",
					"duration_ms":   time.Since(wasmStart).Milliseconds(),
				})
			}
			return nil, fmt.Errorf("VRAXTER SHIELD: filesystem policy violation on mount '%s': %s", qm.guestPath, v)
		}
	}

	if !isDryRun {
		rn.recordAudit(execCtx, audit.EventWASMExecEnd, map[string]interface{}{
			"skill_id":      manifest.ID,
			"wasm_sha256":   currentHash,
			"trust_tier":    int(manifest.Tier),
			"signer_key_id": signerKeyID,
			"exit_status":   "completed",
			"duration_ms":   time.Since(wasmStart).Milliseconds(),
		})
	}

	// Check if output is via file (confined strictly inside the matched mount using os.OpenRoot)
	if !isDryRun && manifest.ManifestV1 != nil && manifest.ManifestV1.IO != nil && strings.ToLower(manifest.ManifestV1.IO.Output.Via) == "file" {
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
