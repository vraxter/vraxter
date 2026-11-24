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
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/utils"
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
	if err == nil || !strings.Contains(err.Error(), "lacks cryptographic signer trust") {
		t.Fatalf("expected Tier3Unverified trust block, got: %v", err)
	}

	// Unset Tier (0) must also fail closed for privileged skills
	manifestZero := manifest
	manifestZero.Tier = 0
	_, err = runner.Execute(context.Background(), manifestZero, nil)
	if err == nil || !strings.Contains(err.Error(), "lacks cryptographic signer trust") {
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

	bigURL, _ := url.Parse(bigSrv.URL)
	bigPort, _ := strconv.Atoi(bigURL.Port())
	redirURL, _ := url.Parse(redirectSrv.URL)
	redirPort, _ := strconv.Atoi(redirURL.Port())

	manifestWithNet := types.SkillManifest{
		ID:          "net-skill",
		Permissions: []string{"network"},
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "net-skill",
			Name:            "Net Skill",
			Version:         "1.0.0",
			Description:     "Network test skill",
			Module:          types.ModuleSpec{File: "net.wasm"},
			Capabilities: &types.CapabilitiesSpec{
				Network: &types.NetworkSpec{
					Mode:         "fetch",
					AllowedPorts: []int{bigPort, redirPort, 80},
				},
			},
		},
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

// --- Phase 1 / Batch 1.1 Security Tests ---

// encodeWASMWithImport constructs a minimal valid WebAssembly binary importing a single function.
func encodeWASMWithImport(modName, fnName string) []byte {
	var out []byte
	out = append(out, 0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00)
	// Type section (id=1): 1 function type () -> ()
	typeSec := []byte{0x01, 0x60, 0x00, 0x00}
	out = append(out, 0x01, byte(len(typeSec)))
	out = append(out, typeSec...)
	// Import section (id=2): 1 function import (modName.fnName)
	var impSec []byte
	impSec = append(impSec, 0x01)
	impSec = append(impSec, byte(len(modName)))
	impSec = append(impSec, []byte(modName)...)
	impSec = append(impSec, byte(len(fnName)))
	impSec = append(impSec, []byte(fnName)...)
	impSec = append(impSec, 0x00, 0x00)
	out = append(out, 0x02, byte(len(impSec)))
	out = append(out, impSec...)
	return out
}

// encodeWASMWithImportedMemory constructs a minimal valid WebAssembly binary importing linear memory.
func encodeWASMWithImportedMemory() []byte {
	var out []byte
	out = append(out, 0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00)
	modName := "env"
	memName := "memory"
	var impSec []byte
	impSec = append(impSec, 0x01)
	impSec = append(impSec, byte(len(modName)))
	impSec = append(impSec, []byte(modName)...)
	impSec = append(impSec, byte(len(memName)))
	impSec = append(impSec, []byte(memName)...)
	// kind=memory (0x02), limits flag=0x00 (min only), min=1 page
	impSec = append(impSec, 0x02, 0x00, 0x01)
	out = append(out, 0x02, byte(len(impSec)))
	out = append(out, impSec...)
	return out
}

// encodeWASMWithInitialMemoryPages constructs a minimal valid WebAssembly binary declaring
// an internal linear memory of minPages (each 64 KB) so WithMemoryLimitPages can be tested directly.
func encodeWASMWithInitialMemoryPages(minPages byte) []byte {
	var out []byte
	out = append(out, 0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00)
	// Memory section (id=5): 1 memory, flag=0x00 (min only), min=minPages
	memSec := []byte{0x01, 0x00, minPages}
	out = append(out, 0x05, byte(len(memSec)))
	out = append(out, memSec...)
	return out
}

func TestRunner_StaticImportVerification_RejectsOverPrivilegedWASM(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner()
	defer runner.Close(context.Background())

	// 1. Real Go WASM module importing vrax_sandbox.http_get MUST be rejected without network capability.
	httpSrc := `package main
import (
	"fmt"
	"unsafe"
)
//go:wasmimport vrax_sandbox http_get
func hostHttpGet(urlPtr, urlLen, outPtr, outMax uint32) uint32
func main() {
	u := []byte("http://127.0.0.1")
	out := make([]byte, 16)
	_ = hostHttpGet(uint32(uintptr(unsafe.Pointer(&u[0]))), uint32(len(u)), uint32(uintptr(unsafe.Pointer(&out[0]))), 16)
	fmt.Print("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"ok\"}}")
}
`
	httpWasmPath, _ := compileTestWASM(t, dir, "netimp", httpSrc)
	if _, err := runner.Execute(context.Background(), types.SkillManifest{
		ID:      "net-undeclared",
		Engine:  "wasm",
		Command: httpWasmPath,
		Tier:    types.Tier2CommunityVerified,
	}, nil); err == nil || !strings.Contains(err.Error(), "vrax_sandbox.http_get") {
		t.Fatalf("expected static rejection for undeclared vrax_sandbox.http_get import, got: %v", err)
	}

	// 2. Real Go WASM module importing path_unlink_file (os.Remove) MUST be rejected without rw FS mount.
	unlinkSrc := `package main
import (
	"fmt"
	"os"
)
func main() {
	_ = os.Remove("/output/victim.txt")
	fmt.Print("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"done\"}}")
}
`
	unlinkWasmPath, _ := compileTestWASM(t, dir, "unlinkimp", unlinkSrc)

	// 2a. Zero FS capabilities -> rejected
	if _, err := runner.Execute(context.Background(), types.SkillManifest{
		ID:      "unlink-no-fs",
		Engine:  "wasm",
		Command: unlinkWasmPath,
		Tier:    types.Tier2CommunityVerified,
	}, nil); err == nil || !strings.Contains(err.Error(), "path_unlink_file") && !strings.Contains(err.Error(), "path_remove_directory") {
		t.Fatalf("expected static rejection for path_unlink_file without FS mounts, got: %v", err)
	}

	// 2b. Read-only FS mount -> STILL rejected because unlink requires writable (rw) mount
	if _, err := runner.Execute(context.Background(), types.SkillManifest{
		ID:      "unlink-ro-fs",
		Engine:  "wasm",
		Command: unlinkWasmPath,
		Tier:    types.Tier2CommunityVerified,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "unlink-ro-fs",
			Name:            "Unlink RO",
			Version:         "1.0.0",
			Description:     "Attempts unlink with ro mount",
			Module:          types.ModuleSpec{File: "unlinkimp.wasm"},
			Capabilities: &types.CapabilitiesSpec{
				FS: &types.FSSpec{
					Mounts: []types.MountSpec{
						{Guest: "/output", Source: "ephemeral", Mode: "ro"},
					},
				},
			},
		},
	}, nil); err == nil || (!strings.Contains(err.Error(), "path_unlink_file") && !strings.Contains(err.Error(), "path_remove_directory")) {
		t.Fatalf("expected static rejection for unlink import on read-only mount, got: %v", err)
	}

	// 3. Unknown host module (fail-closed)
	unknownWasm := encodeWASMWithImport("evil_host", "shell_exec")
	if err := VerifyWASMBytes(context.Background(), unknownWasm, types.SkillManifest{ID: "evil"}); err == nil || !strings.Contains(err.Error(), "unknown host module") {
		t.Fatalf("expected fail-closed rejection for unknown host module, got: %v", err)
	}

	// 4. Disallowed WASI function (proc_raise) and raw socket without allow_raw_sockets
	procRaiseWasm := encodeWASMWithImport("wasi_snapshot_preview1", "proc_raise")
	if err := VerifyWASMBytes(context.Background(), procRaiseWasm, types.SkillManifest{ID: "raise"}); err == nil || !strings.Contains(err.Error(), "disallowed WASM import") {
		t.Fatalf("expected fail-closed rejection for proc_raise, got: %v", err)
	}

	sockWasm := encodeWASMWithImport("wasi_snapshot_preview1", "sock_send")
	netNoRawManifest := types.SkillManifest{
		ID: "sock-no-raw",
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "sock-no-raw",
			Name:            "Sock",
			Version:         "1.0.0",
			Description:     "No raw sockets",
			Module:          types.ModuleSpec{File: "sock.wasm"},
			Capabilities: &types.CapabilitiesSpec{
				Network: &types.NetworkSpec{Mode: "fetch", AllowRawSockets: false},
			},
		},
	}
	if err := VerifyWASMBytes(context.Background(), sockWasm, netNoRawManifest); err == nil || !strings.Contains(err.Error(), "allow_raw_sockets") {
		t.Fatalf("expected rejection for sock_send without allow_raw_sockets, got: %v", err)
	}

	// 5. Imported external linear memory MUST be rejected
	impMemWasm := encodeWASMWithImportedMemory()
	if err := VerifyWASMBytes(context.Background(), impMemWasm, types.SkillManifest{ID: "imp-mem"}); err == nil || !strings.Contains(err.Error(), "external linear memory") {
		t.Fatalf("expected rejection for imported external memory, got: %v", err)
	}
}

func TestHTTPGetFn_PortsWildcardsCIDRAndAntiTOCTOUPinning(t *testing.T) {
	// 1. Default AllowedPorts blocks non-80/443 ports; explicit AllowedPorts restricts strictly to declared list.
	t.Run("AllowedPorts enforcement", func(t *testing.T) {
		defaultNetManifest := types.SkillManifest{
			ID: "port-default",
			ManifestV1: &types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "port-default",
				Name:            "Port Default",
				Version:         "1.0.0",
				Description:     "Default ports only",
				Module:          types.ModuleSpec{File: "p.wasm"},
				Capabilities: &types.CapabilitiesSpec{
					Network: &types.NetworkSpec{Mode: "fetch"},
				},
			},
		}
		if err := validateHTTPGetTarget("http://127.0.0.1:8080/health", defaultNetManifest, networkPolicy{isolationMode: "airgap"}); err == nil || !strings.Contains(err.Error(), "blocked by default") {
			t.Fatalf("expected port 8080 to be blocked by default, got: %v", err)
		}
		if err := validateHTTPGetTarget("http://127.0.0.1/health", defaultNetManifest, networkPolicy{isolationMode: "airgap"}); err != nil {
			t.Fatalf("expected default port 80 to be allowed, got: %v", err)
		}

		explicitPortManifest := types.SkillManifest{
			ID: "port-explicit",
			ManifestV1: &types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "port-explicit",
				Name:            "Port Explicit",
				Version:         "1.0.0",
				Description:     "Explicit port 8443",
				Module:          types.ModuleSpec{File: "p.wasm"},
				Capabilities: &types.CapabilitiesSpec{
					Network: &types.NetworkSpec{Mode: "fetch", AllowedPorts: []int{8443}},
				},
			},
		}
		if err := validateHTTPGetTarget("https://127.0.0.1:8443/api", explicitPortManifest, networkPolicy{isolationMode: "airgap"}); err != nil {
			t.Fatalf("expected declared port 8443 to pass, got: %v", err)
		}
		if err := validateHTTPGetTarget("https://127.0.0.1/api", explicitPortManifest, networkPolicy{isolationMode: "airgap"}); err == nil || !strings.Contains(err.Error(), "allowed_ports") {
			t.Fatalf("expected undeclared port 443 to be rejected when allowed_ports=[8443], got: %v", err)
		}
	})

	// 2. Wildcards (*.example.com) and CIDR ranges in AllowedHosts
	t.Run("Safe wildcards and CIDR matching", func(t *testing.T) {
		mockPublicLookup := func(_ context.Context, host string) ([]net.IP, error) {
			return []net.IP{net.ParseIP("93.184.216.34")}, nil
		}
		policy := networkPolicy{isolationMode: "hybrid"}

		wildcardManifest := types.SkillManifest{
			ID: "wc-skill",
			ManifestV1: &types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "wc-skill",
				Name:            "WC",
				Version:         "1.0.0",
				Description:     "Wildcard test",
				Module:          types.ModuleSpec{File: "wc.wasm"},
				Capabilities: &types.CapabilitiesSpec{
					Network: &types.NetworkSpec{
						Mode:         "fetch",
						AllowedHosts: []string{"*.example.com"},
					},
				},
			},
		}
		if _, err := resolveAndValidateHTTPGetTarget(context.Background(), "https://api.example.com/v1", wildcardManifest, policy, mockPublicLookup, false, false); err != nil {
			t.Fatalf("expected api.example.com to match *.example.com, got: %v", err)
		}
		if _, err := resolveAndValidateHTTPGetTarget(context.Background(), "https://example.com/v1", wildcardManifest, policy, mockPublicLookup, false, false); err == nil {
			t.Fatal("expected bare apex example.com NOT to match *.example.com")
		}
		if _, err := resolveAndValidateHTTPGetTarget(context.Background(), "https://notexample.com/v1", wildcardManifest, policy, mockPublicLookup, false, false); err == nil {
			t.Fatal("expected notexample.com NOT to match *.example.com")
		}

		// Unsafe wildcards MUST be rejected
		for _, badPat := range []string{"*", "*.com", "*.co.uk", "*.*.example.com"} {
			badManifest := types.SkillManifest{
				ID: "bad-wc",
				ManifestV1: &types.SkillManifestV1{
					ManifestVersion: types.ManifestVersionV1,
					ID:              "bad-wc",
					Name:            "Bad WC",
					Version:         "1.0.0",
					Description:     "Unsafe wildcard",
					Module:          types.ModuleSpec{File: "wc.wasm"},
					Capabilities: &types.CapabilitiesSpec{
						Network: &types.NetworkSpec{Mode: "fetch", AllowedHosts: []string{badPat}},
					},
				},
			}
			if _, err := resolveAndValidateHTTPGetTarget(context.Background(), "https://api.example.com/v1", badManifest, policy, mockPublicLookup, false, false); err == nil {
				t.Fatalf("expected unsafe wildcard %q to be rejected", badPat)
			}
		}

		// CIDR matching
		cidrManifest := types.SkillManifest{
			ID: "cidr-skill",
			ManifestV1: &types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "cidr-skill",
				Name:            "CIDR",
				Version:         "1.0.0",
				Description:     "CIDR test",
				Module:          types.ModuleSpec{File: "c.wasm"},
				Capabilities: &types.CapabilitiesSpec{
					Network: &types.NetworkSpec{Mode: "fetch", AllowedHosts: []string{"10.10.0.0/16"}},
				},
			},
		}
		// Strict airgap rejects unwhitelisted RFC 1918 LAN IPs even when in manifest allowed_hosts
		if _, err := resolveAndValidateHTTPGetTarget(context.Background(), "http://10.10.5.42/status", cidrManifest, networkPolicy{isolationMode: "airgap"}, nil, false, false); err == nil {
			t.Fatal("expected unwhitelisted private IP 10.10.5.42 to be blocked under strict airgap")
		}
		// Explicitly whitelisted CIDR permits 10.10.5.42 under strict airgap while rejecting 10.20.5.42
		if _, err := resolveAndValidateHTTPGetTarget(context.Background(), "http://10.10.5.42/status", cidrManifest, networkPolicy{isolationMode: "airgap", whitelistedIPs: []string{"10.10.0.0/16"}}, nil, false, false); err != nil {
			t.Fatalf("expected whitelisted 10.10.5.42 to match 10.10.0.0/16 under airgap, got: %v", err)
		}
		// local_lan permits RFC 1918 CIDR match and rejects IPs outside manifest allowed_hosts
		if _, err := resolveAndValidateHTTPGetTarget(context.Background(), "http://10.10.5.42/status", cidrManifest, networkPolicy{isolationMode: "local_lan"}, nil, false, false); err != nil {
			t.Fatalf("expected 10.10.5.42 to match 10.10.0.0/16 under local_lan, got: %v", err)
		}
		if _, err := resolveAndValidateHTTPGetTarget(context.Background(), "http://10.20.5.42/status", cidrManifest, networkPolicy{isolationMode: "local_lan"}, nil, false, false); err == nil {
			t.Fatal("expected 10.20.5.42 to be rejected outside 10.10.0.0/16")
		}
	})

	// 3. Anti-TOCTOU DNS pinning: lookup called ONCE, dial uses exact pinned IP, Host header preserved,
	//    and redirect to private IP is blocked.
	t.Run("Single-lookup IP pinning and redirect SSRF defense", func(t *testing.T) {
		var observedHostHeader string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			observedHostHeader = r.Host
			if r.URL.Path == "/rebind-redirect" {
				http.Redirect(w, r, "http://10.0.0.5/internal-admin", http.StatusFound)
				return
			}
			_, _ = w.Write([]byte("PINNED_OK"))
		}))
		defer srv.Close()

		var lookupCount atomic.Int32
		var dialedAddr string

		// Simulated DNS rebinding server: 1st call returns allowed public IP 93.184.216.34;
		// any 2nd call would return AWS IMDS 169.254.169.254.
		rebindLookup := func(_ context.Context, host string) ([]net.IP, error) {
			n := lookupCount.Add(1)
			if n == 1 {
				return []net.IP{net.ParseIP("93.184.216.34")}, nil
			}
			return []net.IP{net.ParseIP("169.254.169.254")}, nil
		}

		// Intercept pinned DialContext to record exact pinned address and forward socket to local test server.
		srvAddr := srv.Listener.Addr().String()
		customDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
			dialedAddr = addr
			var d net.Dialer
			return d.DialContext(ctx, network, srvAddr)
		}

		manifest := types.SkillManifest{
			ID: "pin-skill",
			ManifestV1: &types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "pin-skill",
				Name:            "Pin",
				Version:         "1.0.0",
				Description:     "Pin test",
				Module:          types.ModuleSpec{File: "pin.wasm"},
				Capabilities: &types.CapabilitiesSpec{
					Network: &types.NetworkSpec{
						Mode:         "fetch",
						AllowedHosts: []string{"*.example.com"},
					},
				},
			},
		}

		ctx := context.Background()
		ctx = context.WithValue(ctx, manifestKey, manifest)
		ctx = context.WithValue(ctx, networkPolicyKey, networkPolicy{isolationMode: "hybrid"})
		ctx = context.WithValue(ctx, httpLookupCtxKey, utils.LookupIPFunc(rebindLookup))
		ctx = context.WithValue(ctx, httpDialCtxKey, dialContextFunc(customDial))

		targetURL := "http://api.example.com/data"
		mem := &mockMemory{buf: make([]byte, 4096)}
		copy(mem.buf[0:], []byte(targetURL))
		n := httpGetFn(ctx, &mockModule{mem: mem}, 0, uint32(len(targetURL)), 256, 256)
		if n != uint32(len("PINNED_OK")) || string(mem.buf[256:256+n]) != "PINNED_OK" {
			t.Fatalf("expected PINNED_OK, got n=%d body=%q", n, string(mem.buf[256:256+n]))
		}
		if got := lookupCount.Load(); got != 1 {
			t.Fatalf("TOCTOU vulnerability: expected exactly 1 DNS lookup, got %d", got)
		}
		if dialedAddr != "93.184.216.34:80" {
			t.Fatalf("expected DialContext to dial pinned IP 93.184.216.34:80, got %q", dialedAddr)
		}
		if observedHostHeader != "api.example.com" {
			t.Fatalf("expected original Host header 'api.example.com', got %q", observedHostHeader)
		}

		// Redirect from external api.example.com -> private 10.0.0.5 MUST be blocked even without allowed_hosts!
		noHostFilterManifest := types.SkillManifest{
			ID: "redir-ssrf",
			ManifestV1: &types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "redir-ssrf",
				Name:            "Redir SSRF",
				Version:         "1.0.0",
				Description:     "No host filter",
				Module:          types.ModuleSpec{File: "r.wasm"},
				Capabilities: &types.CapabilitiesSpec{
					Network: &types.NetworkSpec{Mode: "fetch"},
				},
			},
		}
		lookupCount.Store(0)
		ctx2 := context.Background()
		ctx2 = context.WithValue(ctx2, manifestKey, noHostFilterManifest)
		ctx2 = context.WithValue(ctx2, networkPolicyKey, networkPolicy{isolationMode: "hybrid"})
		ctx2 = context.WithValue(ctx2, httpLookupCtxKey, utils.LookupIPFunc(rebindLookup))
		ctx2 = context.WithValue(ctx2, httpDialCtxKey, dialContextFunc(customDial))

		redirURL := "http://api.example.com/rebind-redirect"
		mem2 := &mockMemory{buf: make([]byte, 4096)}
		copy(mem2.buf[0:], []byte(redirURL))
		if got := httpGetFn(ctx2, &mockModule{mem: mem2}, 0, uint32(len(redirURL)), 256, 256); got != 0 {
			t.Fatalf("expected redirect from external host to private IP 10.0.0.5 to be blocked, got %d bytes", got)
		}
	})
}

func TestRunner_MemoryLimitPagesEnforcesDeterministicTrap(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner()
	defer runner.Close(context.Background())

	// 1. Minimal raw WASM declaring 32 pages (2 MB) under a 1 MB (16 pages) manifest limit MUST fail deterministically.
	twoMBPath := filepath.Join(dir, "two_mb.wasm")
	if err := os.WriteFile(twoMBPath, encodeWASMWithInitialMemoryPages(32), 0600); err != nil {
		t.Fatalf("write raw wasm: %v", err)
	}
	oneMBManifest := types.SkillManifest{
		ID:      "mem-raw-limit",
		Engine:  "wasm",
		Command: twoMBPath,
		Tier:    types.Tier3Unverified,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "mem-raw-limit",
			Name:            "Mem Raw",
			Version:         "1.0.0",
			Description:     "1 MB memory ceiling",
			Module:          types.ModuleSpec{File: "two_mb.wasm"},
			Limits:          &types.LimitsSpec{MemoryMB: 1},
		},
	}
	if _, err := runner.Execute(context.Background(), oneMBManifest, nil); err == nil {
		t.Fatal("expected deterministic memory limit rejection for 2 MB WASM under 1 MB limit")
	}

	// 2. Real Go WASM allocating 32 MB under a 16 MB limit MUST trap/crash cleanly without OOMing host.
	oomSrc := `package main
import "fmt"
func main() {
	buf := make([]byte, 32*1024*1024)
	for i := 0; i < len(buf); i += 4096 {
		buf[i] = byte(i)
	}
	fmt.Printf("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"%d\"}}", buf[len(buf)-1])
}
`
	oomWasmPath, _ := compileTestWASM(t, dir, "oombomb", oomSrc)
	cappedManifest := types.SkillManifest{
		ID:      "mem-go-limit",
		Engine:  "wasm",
		Command: oomWasmPath,
		Tier:    types.Tier3Unverified,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "mem-go-limit",
			Name:            "Mem Go",
			Version:         "1.0.0",
			Description:     "16 MB ceiling against 32 MB allocation",
			Module:          types.ModuleSpec{File: "oombomb.wasm"},
			Limits:          &types.LimitsSpec{MemoryMB: 16},
		},
	}
	if _, err := runner.Execute(context.Background(), cappedManifest, nil); err == nil {
		t.Fatal("expected Go WASM allocating 32 MB to fail deterministically under 16 MB Limits.MemoryMB")
	}
}

func TestRunner_FSQuotas_ExtensionsMaxFileMaxTotalMaxFilesAndAllowDelete(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner()
	defer runner.Close(context.Background())

	// Single parameterized Go WASM skill performing FS operations selected via stdin JSON-RPC params.
	fsGuestSrc := `package main
import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)
type req struct {
	Params map[string]any ` + "`json:\"params\"`" + `
}
func main() {
	raw, _ := io.ReadAll(os.Stdin)
	var r req
	_ = json.Unmarshal(raw, &r)
	action, _ := r.Params["action"].(string)
	var opErr error
	switch action {
	case "write_ok":
		opErr = os.WriteFile("/output/report.json", []byte("{\"ok\":true}"), 0644)
	case "write_bad_ext":
		opErr = os.WriteFile("/output/payload.sh", []byte("#!/bin/sh\necho pwned"), 0644)
	case "write_oversized":
		opErr = os.WriteFile("/output/big.txt", []byte(strings.Repeat("A", 4096)), 0644)
	case "write_two_files_total":
		if err := os.WriteFile("/output/f1.txt", []byte(strings.Repeat("B", 1500)), 0644); err != nil {
			opErr = err
		} else {
			opErr = os.WriteFile("/output/f2.txt", []byte(strings.Repeat("C", 1500)), 0644)
		}
	case "write_three_files":
		for i := 1; i <= 3; i++ {
			if err := os.WriteFile(fmt.Sprintf("/output/file%d.txt", i), []byte("hi"), 0644); err != nil {
				opErr = err
				break
			}
		}
	case "unlink_keep":
		opErr = os.Remove("/output/keep.txt")
	case "truncate_keep":
		opErr = os.Truncate("/output/keep.txt", 0)
	}
	if opErr != nil {
		fmt.Printf("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"ERR:%s\"}}", opErr.Error())
		return
	}
	fmt.Print("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"OK\"}}")
}
`
	fsWasmPath, _ := compileTestWASM(t, dir, "fsquota", fsGuestSrc)

	makeManifest := func(mount types.MountSpec) types.SkillManifest {
		return types.SkillManifest{
			ID:      "fs-quota-skill",
			Engine:  "wasm",
			Command: fsWasmPath,
			Tier:    types.Tier2CommunityVerified,
			ManifestV1: &types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "fs-quota-skill",
				Name:            "FS Quota",
				Version:         "1.0.0",
				Description:     "Tests FS quotas",
				Module:          types.ModuleSpec{File: "fsquota.wasm"},
				Capabilities: &types.CapabilitiesSpec{
					FS: &types.FSSpec{Mounts: []types.MountSpec{mount}},
				},
			},
		}
	}

	// 1. allow_extensions: .json allowed, .sh blocked
	t.Run("allow_extensions", func(t *testing.T) {
		outDir := t.TempDir()
		ctx := WithSessionDirs(context.Background(), SessionDirs{OutputDir: outDir})
		m := makeManifest(types.MountSpec{
			Guest: "/output", Source: "output", Mode: "rw",
			AllowExtensions: []string{".json", ".txt"},
		})
		res, err := runner.Execute(ctx, m, map[string]interface{}{"action": "write_ok"})
		if err != nil || res.Output != "OK" {
			t.Fatalf("expected allowed .json write to succeed, got res=%+v err=%v", res, err)
		}
		if _, err := runner.Execute(ctx, m, map[string]interface{}{"action": "write_bad_ext"}); err == nil || !strings.Contains(err.Error(), "allow_extensions") {
			t.Fatalf("expected .sh drop to be blocked by allow_extensions, got: %v", err)
		}
		if _, statErr := os.Stat(filepath.Join(outDir, "payload.sh")); !os.IsNotExist(statErr) {
			t.Fatal("disallowed payload.sh must not exist on host filesystem")
		}
	})

	// 2. max_file_mb: 0.002 MB (~2097 bytes) blocks 4096-byte file write
	t.Run("max_file_mb", func(t *testing.T) {
		outDir := t.TempDir()
		ctx := WithSessionDirs(context.Background(), SessionDirs{OutputDir: outDir})
		m := makeManifest(types.MountSpec{
			Guest: "/output", Source: "output", Mode: "rw",
			MaxFileMB: 0.002,
		})
		if _, err := runner.Execute(ctx, m, map[string]interface{}{"action": "write_oversized"}); err == nil || !strings.Contains(err.Error(), "max_file_mb") {
			t.Fatalf("expected 4096-byte write to fail max_file_mb quota, got: %v", err)
		}
		if fi, statErr := os.Stat(filepath.Join(outDir, "big.txt")); statErr == nil && fi.Size() > 2097 {
			t.Fatalf("host file exceeded max_file_mb limit: %d bytes", fi.Size())
		}
	})

	// 3. max_total_mb: cumulative bytes across multiple files capped at ~2097 bytes
	t.Run("max_total_mb", func(t *testing.T) {
		outDir := t.TempDir()
		ctx := WithSessionDirs(context.Background(), SessionDirs{OutputDir: outDir})
		m := makeManifest(types.MountSpec{
			Guest: "/output", Source: "output", Mode: "rw",
			MaxFileMB:  0.002,
			MaxTotalMB: 0.002,
		})
		if _, err := runner.Execute(ctx, m, map[string]interface{}{"action": "write_two_files_total"}); err == nil || !strings.Contains(err.Error(), "max_total_mb") {
			t.Fatalf("expected cumulative writes to fail max_total_mb quota, got: %v", err)
		}
	})

	// 4. max_files: limit 2 files blocks creation of 3rd file
	t.Run("max_files", func(t *testing.T) {
		outDir := t.TempDir()
		ctx := WithSessionDirs(context.Background(), SessionDirs{OutputDir: outDir})
		m := makeManifest(types.MountSpec{
			Guest: "/output", Source: "output", Mode: "rw",
			MaxFiles: 2,
		})
		if _, err := runner.Execute(ctx, m, map[string]interface{}{"action": "write_three_files"}); err == nil || !strings.Contains(err.Error(), "max_files") {
			t.Fatalf("expected 3rd file to fail max_files=2 quota, got: %v", err)
		}
		entries, _ := os.ReadDir(outDir)
		if len(entries) != 2 {
			t.Fatalf("expected exactly 2 files created under max_files=2, got %d", len(entries))
		}
	})

	// 5. allow_delete: false blocks Unlink and destructive Truncate; true allows deletion
	t.Run("allow_delete", func(t *testing.T) {
		outDir := t.TempDir()
		ctx := WithSessionDirs(context.Background(), SessionDirs{OutputDir: outDir})
		keepPath := filepath.Join(outDir, "keep.txt")
		if err := os.WriteFile(keepPath, []byte("IMPORTANT_DATA"), 0600); err != nil {
			t.Fatalf("seed keep.txt: %v", err)
		}

		noDelManifest := makeManifest(types.MountSpec{
			Guest: "/output", Source: "output", Mode: "rw",
			AllowDelete: false,
		})
		if _, err := runner.Execute(ctx, noDelManifest, map[string]interface{}{"action": "unlink_keep"}); err == nil || !strings.Contains(err.Error(), "allow_delete") {
			t.Fatalf("expected Unlink to be blocked when allow_delete=false, got: %v", err)
		}
		if _, err := runner.Execute(ctx, noDelManifest, map[string]interface{}{"action": "truncate_keep"}); err == nil || !strings.Contains(err.Error(), "allow_delete") {
			t.Fatalf("expected destructive Truncate to be blocked when allow_delete=false, got: %v", err)
		}
		stillThere, err := os.ReadFile(keepPath)
		if err != nil || string(stillThere) != "IMPORTANT_DATA" {
			t.Fatalf("keep.txt was altered or deleted despite allow_delete=false: data=%q err=%v", string(stillThere), err)
		}

		// With allow_delete=true, unlink succeeds cleanly
		delManifest := makeManifest(types.MountSpec{
			Guest: "/output", Source: "output", Mode: "rw",
			AllowDelete: true,
		})
		res, err := runner.Execute(ctx, delManifest, map[string]interface{}{"action": "unlink_keep"})
		if err != nil || res.Output != "OK" {
			t.Fatalf("expected Unlink to succeed when allow_delete=true, got res=%+v err=%v", res, err)
		}
		if _, statErr := os.Stat(keepPath); !os.IsNotExist(statErr) {
			t.Fatal("expected keep.txt to be removed when allow_delete=true")
		}
	})
}

func TestRunner_ConcurrencySemaphoreCapsSimultaneousExecs(t *testing.T) {
	dir := t.TempDir()
	runner := NewRunner()
	defer runner.Close(context.Background())

	if runner.MaxConcurrentExecs() != DefaultMaxConcurrentExecs {
		t.Fatalf("expected default MaxConcurrentExecs=%d, got %d", DefaultMaxConcurrentExecs, runner.MaxConcurrentExecs())
	}
	runner.SetMaxConcurrentExecs(2)

	enteredCh := make(chan struct{}, 4)
	releaseCh := make(chan struct{})
	var active atomic.Int32
	var maxObserved atomic.Int32

	holdSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := active.Add(1)
		for {
			prev := maxObserved.Load()
			if cur <= prev || maxObserved.CompareAndSwap(prev, cur) {
				break
			}
		}
		enteredCh <- struct{}{}
		<-releaseCh
		active.Add(-1)
		_, _ = w.Write([]byte("OK"))
	}))
	defer holdSrv.Close()

	u, _ := url.Parse(holdSrv.URL)
	srvPort, _ := strconv.Atoi(u.Port())

	guestSrc := fmt.Sprintf(`package main
import (
	"fmt"
	"unsafe"
)
//go:wasmimport vrax_sandbox http_get
func hostHttpGet(urlPtr, urlLen, outPtr, outMax uint32) uint32
func main() {
	urlStr := %q
	uBytes := []byte(urlStr)
	out := make([]byte, 32)
	n := hostHttpGet(uint32(uintptr(unsafe.Pointer(&uBytes[0]))), uint32(len(uBytes)), uint32(uintptr(unsafe.Pointer(&out[0]))), 32)
	fmt.Printf("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"%%s\"}}", string(out[:n]))
}
`, holdSrv.URL)

	wasmPath, _ := compileTestWASM(t, dir, "semhold", guestSrc)
	manifest := types.SkillManifest{
		ID:      "sem-skill",
		Engine:  "wasm",
		Command: wasmPath,
		Tier:    types.Tier2CommunityVerified,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "sem-skill",
			Name:            "Sem Skill",
			Version:         "1.0.0",
			Description:     "Holds exec slot via local HTTP",
			Module:          types.ModuleSpec{File: "semhold.wasm"},
			Capabilities: &types.CapabilitiesSpec{
				Network: &types.NetworkSpec{
					Mode:         "fetch",
					AllowedPorts: []int{srvPort},
				},
			},
		},
	}

	// Occupy both semaphore slots (2/2)
	var wg sync.WaitGroup
	slotErrs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := runner.Execute(context.Background(), manifest, nil)
			slotErrs <- err
		}()
	}

	for i := 0; i < 2; i++ {
		select {
		case <-enteredCh:
		case <-time.After(5 * time.Second):
			close(releaseCh)
			t.Fatal("timed out waiting for first 2 WASM instances to enter semaphore")
		}
	}

	// 3rd concurrent execution with a short deadline MUST fail on semaphore acquisition!
	shortCtx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	_, err := runner.Execute(shortCtx, manifest, nil)
	if err == nil || !strings.Contains(err.Error(), "concurrency limit (2) reached") {
		close(releaseCh)
		t.Fatalf("expected 3rd concurrent Execute to fail on semaphore limit, got: %v", err)
	}

	// Release first 2 executions and verify clean completion + semaphore release
	close(releaseCh)
	wg.Wait()
	close(slotErrs)
	for e := range slotErrs {
		if e != nil {
			t.Fatalf("initial slotted Execute failed: %v", e)
		}
	}
	if maxObserved.Load() > 2 {
		t.Fatalf("concurrency limit breached: observed %d simultaneous executions > 2", maxObserved.Load())
	}

	// Subsequent execution after slots released MUST succeed cleanly
	res, err := runner.Execute(context.Background(), manifest, nil)
	if err != nil || res.Output != "OK" {
		t.Fatalf("expected post-release Execute to succeed, got res=%+v err=%v", res, err)
	}
}
