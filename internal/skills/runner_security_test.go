// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vraxter/vraxter/pkg/types"
)

// compileTestWASM compiles a minimal Go WASI (wasip1/wasm) module returning outputJSON on stdout.
func compileTestWASM(t *testing.T, dir, name, goSource string) (string, string) {
	t.Helper()
	srcDir := filepath.Join(dir, "wasm_src_"+name)
	if err := os.MkdirAll(srcDir, 0700); err != nil {
		t.Fatalf("mkdir wasm src: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module "+name+"\n\ngo 1.24\n"), 0600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(goSource), 0600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	outWasm := filepath.Join(dir, name+".wasm")
	cmd := exec.Command("go", "build", "-o", outWasm, ".")
	cmd.Dir = srcDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to compile test wasm module %s: %v\n%s", name, err, string(out))
	}

	raw, err := os.ReadFile(outWasm)
	if err != nil {
		t.Fatalf("read compiled wasm: %v", err)
	}
	sum := sha256.Sum256(raw)
	return outWasm, hex.EncodeToString(sum[:])
}

func TestRunner_CacheReloadsAndEvictsWhenSHA256ChangesOnDisk(t *testing.T) {
	dir := t.TempDir()

	srcV1 := `package main
import "fmt"
func main() {
	fmt.Print("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"VERSION_ONE\"},\"id\":\"cache-test\"}")
}
`
	srcV2 := `package main
import "fmt"
func main() {
	fmt.Print("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"VERSION_TWO\"},\"id\":\"cache-test\"}")
}
`
	v1Path, hashV1 := compileTestWASM(t, dir, "modv1", srcV1)
	v2Path, hashV2 := compileTestWASM(t, dir, "modv2", srcV2)
	if hashV1 == hashV2 {
		t.Fatal("expected distinct SHA-256 hashes for v1 and v2 modules")
	}

	livePath := filepath.Join(dir, "live_skill.wasm")
	v1Bytes, _ := os.ReadFile(v1Path)
	v2Bytes, _ := os.ReadFile(v2Path)
	if err := os.WriteFile(livePath, v1Bytes, 0600); err != nil {
		t.Fatalf("write v1 to livePath: %v", err)
	}

	runner := NewRunner()
	defer runner.Close(context.Background())

	manifest := types.SkillManifest{
		ID:      "cache-test",
		Name:    "Cache Test",
		Engine:  "wasm",
		Command: livePath,
		Tier:    types.Tier3Unverified, // zero capabilities -> sandbox safe
	}

	// 1. First execution loads and caches v1 under hashV1
	res1, err := runner.Execute(context.Background(), manifest, nil)
	if err != nil {
		t.Fatalf("Execute v1 failed: %v", err)
	}
	if res1.Output != "VERSION_ONE" {
		t.Fatalf("expected VERSION_ONE, got %q", res1.Output)
	}
	if !runner.HasCachedHash(hashV1) || runner.CacheSize() != 1 {
		t.Fatalf("expected hashV1 cached (size=1), got hasV1=%v size=%d", runner.HasCachedHash(hashV1), runner.CacheSize())
	}

	// 2. Overwrite exact same filepath on disk with v2 bytes (new SHA-256)
	if err := os.WriteFile(livePath, v2Bytes, 0600); err != nil {
		t.Fatalf("overwrite livePath with v2: %v", err)
	}

	// 3. Second execution MUST detect new SHA-256, recompile v2, and evict/close stale hashV1
	res2, err := runner.Execute(context.Background(), manifest, nil)
	if err != nil {
		t.Fatalf("Execute v2 failed: %v", err)
	}
	if res2.Output != "VERSION_TWO" {
		t.Fatalf("expected VERSION_TWO after on-disk SHA-256 change, got %q", res2.Output)
	}
	if !runner.HasCachedHash(hashV2) {
		t.Fatalf("expected hashV2 to be cached")
	}
	if runner.HasCachedHash(hashV1) {
		t.Fatalf("expected obsolete hashV1 to be evicted from cache")
	}
	if runner.CacheSize() != 1 {
		t.Fatalf("expected cache size 1 after eviction, got %d", runner.CacheSize())
	}

	// 4. Concurrent executions against the cached module should complete safely
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := runner.Execute(context.Background(), manifest, nil)
			if err != nil {
				errs <- err
				return
			}
			if r.Output != "VERSION_TWO" {
				errs <- fmt.Errorf("unexpected output %q", r.Output)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Execute failed: %v", err)
	}
}

func TestRunner_Tier3UnverifiedBlocksPrivilegedSkills(t *testing.T) {
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "dummy.wasm")
	_ = os.WriteFile(wasmPath, []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, 0600)

	runner := NewRunner()
	defer runner.Close(context.Background())

	manifest := types.SkillManifest{
		ID:          "untrusted-net",
		Engine:      "wasm",
		Command:     wasmPath,
		Tier:        types.Tier3Unverified,
		Permissions: []string{"network"},
	}
	_, err := runner.Execute(context.Background(), manifest, nil)
	if err == nil || !strings.Contains(err.Error(), "lacks community trust") {
		t.Fatalf("expected Tier3Unverified trust block, got: %v", err)
	}

	// Unset Tier (0) must also fail closed for privileged skills
	manifestZero := manifest
	manifestZero.Tier = 0
	_, err = runner.Execute(context.Background(), manifestZero, nil)
	if err == nil || !strings.Contains(err.Error(), "lacks community trust") {
		t.Fatalf("expected unset Tier (0) to fail closed, got: %v", err)
	}
}

func TestRunner_BlocksHostMountsAndPathTraversal(t *testing.T) {
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "dummy.wasm")
	_ = os.WriteFile(wasmPath, []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, 0600)

	runner := NewRunner()
	defer runner.Close(context.Background())

	// 1. source: "host" blocked by default in airgap
	hostMountManifest := types.SkillManifest{
		ID:      "host-escape",
		Engine:  "wasm",
		Command: wasmPath,
		Tier:    types.Tier2CommunityVerified,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "host-escape",
			Name:            "Host Escape",
			Version:         "1.0.0",
			Description:     "Attempts host mount",
			Module:          types.ModuleSpec{File: "dummy.wasm"},
			Capabilities: &types.CapabilitiesSpec{
				FS: &types.FSSpec{
					Mounts: []types.MountSpec{
						{Guest: "/host", Source: "host", HostPath: dir, Mode: "ro"},
					},
				},
			},
		},
	}
	if _, err := runner.Execute(context.Background(), hostMountManifest, nil); err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("expected host mount to be blocked in airgap, got: %v", err)
	}

	// Even if SetAllowHostMounts(true) is called, airgap MUST still forbid source: "host"
	runner.SetAllowHostMounts(true)
	if _, err := runner.Execute(context.Background(), hostMountManifest, nil); err == nil || !strings.Contains(err.Error(), "forbidden in airgap") {
		t.Fatalf("expected host mount to remain strictly forbidden in airgap mode, got: %v", err)
	}

	// Outside airgap with AllowHostMounts=false (default), source: "host" MUST still be blocked
	runner.SetAllowHostMounts(false)
	runner.SetNetworkPolicy("hybrid", nil)
	if _, err := runner.Execute(context.Background(), hostMountManifest, nil); err == nil || !strings.Contains(err.Error(), "disabled by default") {
		t.Fatalf("expected host mount to be blocked by default in hybrid mode, got: %v", err)
	}

	// 2. mount.Subpath containing "../" MUST be rejected
	subpathTraversal := types.SkillManifest{
		ID:      "subpath-escape",
		Engine:  "wasm",
		Command: wasmPath,
		Tier:    types.Tier2CommunityVerified,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "subpath-escape",
			Name:            "Subpath Escape",
			Version:         "1.0.0",
			Description:     "Attempts subpath traversal",
			Module:          types.ModuleSpec{File: "dummy.wasm"},
			Capabilities: &types.CapabilitiesSpec{
				FS: &types.FSSpec{
					Mounts: []types.MountSpec{
						{Guest: "/work", Source: "workspace", Subpath: "../../etc", Mode: "ro"},
					},
				},
			},
		},
	}
	if _, err := runner.Execute(context.Background(), subpathTraversal, nil); err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("expected mount.Subpath traversal to be blocked, got: %v", err)
	}

	// 3. io.output.path containing "../" MUST be rejected
	outputTraversal := types.SkillManifest{
		ID:      "output-escape",
		Engine:  "wasm",
		Command: wasmPath,
		Tier:    types.Tier2CommunityVerified,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "output-escape",
			Name:            "Output Escape",
			Version:         "1.0.0",
			Description:     "Attempts output path traversal",
			Module:          types.ModuleSpec{File: "dummy.wasm"},
			IO: &types.IOSpec{
				Output: types.OutputSpec{Via: "file", Path: "/output/../../etc/passwd"},
			},
			Capabilities: &types.CapabilitiesSpec{
				FS: &types.FSSpec{
					Mounts: []types.MountSpec{
						{Guest: "/output", Source: "ephemeral", Mode: "rw"},
					},
				},
			},
		},
	}
	if _, err := runner.Execute(context.Background(), outputTraversal, nil); err == nil || !strings.Contains(err.Error(), "traversal") {
		t.Fatalf("expected io.output.path traversal to be blocked, got: %v", err)
	}
}

func TestLimitedBuffer_CapsAt2MB(t *testing.T) {
	lb := newLimitedBuffer(MaxWASMOutputBytes)
	chunk := make([]byte, 512*1024)
	for i := range chunk {
		chunk[i] = 'X'
	}
	// Write 3 MB total
	for i := 0; i < 6; i++ {
		n, err := lb.Write(chunk)
		if err != nil || n != len(chunk) {
			t.Fatalf("Write should succeed without erroring child writers, got n=%d err=%v", n, err)
		}
	}
	if !lb.Truncated() {
		t.Fatal("expected Truncated() == true")
	}
	if len(lb.Bytes()) != MaxWASMOutputBytes {
		t.Fatalf("expected buffered bytes capped at %d, got %d", MaxWASMOutputBytes, len(lb.Bytes()))
	}
}

func TestHTTPGetFn_LimitReaderAndBlocksDisallowedRedirects(t *testing.T) {
	// 1. Local server returning a huge response -> must be strictly bounded by outMax / LimitReader
	bigSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(strings.Repeat("Z", 128*1024)))
	}))
	defer bigSrv.Close()

	// 2. Local server redirecting to WAN IP 8.8.8.8 -> CheckRedirect MUST block!
	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://8.8.8.8/secret", http.StatusFound)
	}))
	defer redirectSrv.Close()

	manifestWithNet := types.SkillManifest{
		ID:          "net-skill",
		Permissions: []string{"network"},
	}
	ctx := context.WithValue(context.Background(), manifestKey, manifestWithNet)
	ctx = context.WithValue(ctx, networkPolicyKey, networkPolicy{isolationMode: "airgap"})

	// Test bounded read on allowed localhost target
	mem := &mockMemory{buf: make([]byte, 4096)}
	copy(mem.buf[0:], []byte(bigSrv.URL))
	mod := &mockModule{mem: mem}
	n := httpGetFn(ctx, mod, 0, uint32(len(bigSrv.URL)), 256, 64)
	if n != 64 {
		t.Fatalf("expected httpGetFn to read strictly 64 bounded bytes, got %d", n)
	}
	if string(mem.buf[256:256+64]) != strings.Repeat("Z", 64) {
		t.Fatalf("unexpected bounded response bytes")
	}

	// Test blocked redirect from 127.0.0.1 -> 8.8.8.8
	mem2 := &mockMemory{buf: make([]byte, 4096)}
	copy(mem2.buf[0:], []byte(redirectSrv.URL))
	mod2 := &mockModule{mem: mem2}
	nRedir := httpGetFn(ctx, mod2, 0, uint32(len(redirectSrv.URL)), 256, 512)
	if nRedir != 0 {
		t.Fatalf("expected redirect to public IP 8.8.8.8 to be blocked (return 0), got %d bytes", nRedir)
	}
}

func TestMetaSkills_WorkspaceConfinementViaOpenRoot(t *testing.T) {
	parentDir := t.TempDir()
	wsDir := filepath.Join(parentDir, "workspace")
	if err := os.MkdirAll(wsDir, 0700); err != nil {
		t.Fatalf("mkdir wsDir: %v", err)
	}

	// Secret file OUTSIDE workspace
	outsideFile := filepath.Join(parentDir, "secret.txt")
	if err := os.WriteFile(outsideFile, []byte("TOP_SECRET_OUTSIDE"), 0600); err != nil {
		t.Fatalf("write outside secret: %v", err)
	}
	// Normal file INSIDE workspace
	insideFile := filepath.Join(wsDir, "app.go")
	if err := os.WriteFile(insideFile, []byte("package app\n"), 0600); err != nil {
		t.Fatalf("write inside file: %v", err)
	}

	SetWorkspaceRoot(wsDir)
	t.Cleanup(func() { SetWorkspaceRoot("") })

	// 1. Inside relative read works
	if out := ReadFile("app.go"); !strings.Contains(out, "package app") {
		t.Fatalf("expected inside relative ReadFile to succeed, got: %s", out)
	}

	// 2. Relative traversal "../secret.txt" MUST be blocked on ReadFile, ListDir, PatchCode
	if out := ReadFile("../secret.txt"); !strings.HasPrefix(out, "vraxter-read-file ERROR") || strings.Contains(out, "TOP_SECRET_OUTSIDE") {
		t.Fatalf("expected ReadFile('../secret.txt') to be blocked, got: %s", out)
	}
	if out := ListDir(".."); !strings.HasPrefix(out, "vraxter-list-dir ERROR") || strings.Contains(out, "secret.txt") {
		t.Fatalf("expected ListDir('..') to be blocked, got: %s", out)
	}
	if out := PatchCode("../secret.txt", "TOP_SECRET_OUTSIDE", "PWNED"); !strings.Contains(out, "ERROR") {
		t.Fatalf("expected PatchCode('../secret.txt') to be blocked, got: %s", out)
	}

	// 3. Absolute path outside workspace MUST be blocked
	if out := ReadFile(outsideFile); !strings.HasPrefix(out, "vraxter-read-file ERROR") || strings.Contains(out, "TOP_SECRET_OUTSIDE") {
		t.Fatalf("expected ReadFile(outsideFile) to be blocked, got: %s", out)
	}
	if out := PatchCode(outsideFile, "TOP_SECRET_OUTSIDE", "PWNED"); !strings.Contains(out, "ERROR") {
		t.Fatalf("expected PatchCode(outsideFile) to be blocked, got: %s", out)
	}

	// 4. Home directory shortcut "~" MUST be blocked
	if out := ReadFile("~/.ssh/id_rsa"); !strings.HasPrefix(out, "vraxter-read-file ERROR") {
		t.Fatalf("expected ReadFile('~/.ssh/id_rsa') to be blocked, got: %s", out)
	}

	// Verify outside file was never modified
	stillSecret, _ := os.ReadFile(outsideFile)
	if string(stillSecret) != "TOP_SECRET_OUTSIDE" {
		t.Fatalf("outside file was unexpectedly modified: %q", string(stillSecret))
	}
}
