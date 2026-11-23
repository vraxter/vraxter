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
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/types"
)

func TestGoHTTPSDKCode_MatchesVraxSandboxAndGuardsEmptyURL(t *testing.T) {
	if !strings.Contains(GoHTTPSDKCode, "//go:wasmimport vrax_sandbox http_get") {
		t.Fatalf("GoHTTPSDKCode must import vrax_sandbox http_get, got:\n%s", GoHTTPSDKCode)
	}
	if strings.Contains(GoHTTPSDKCode, "vrax_v1") {
		t.Fatalf("GoHTTPSDKCode must not reference obsolete vrax_v1 module")
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go binary not in PATH; skipping WASM compile integration check")
	}

	// Compile a real WASM skill using GoHTTPSDKCode that calls HttpGet("") and prints JSON-RPC result.
	buildDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(buildDir, "go.mod"), []byte("module testhttpsdk\n\ngo 1.24\n"), 0600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "vrax.go"), []byte(GoHTTPSDKCode), 0600); err != nil {
		t.Fatalf("write vrax.go: %v", err)
	}
	mainSrc := `package main

import "fmt"

func main() {
	res := HttpGet("")
	fmt.Printf("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"empty_len=%d\"}}", len(res))
}
`
	if err := os.WriteFile(filepath.Join(buildDir, "main.go"), []byte(mainSrc), 0600); err != nil {
		t.Fatalf("write main.go: %v", err)
	}

	wasmPath := filepath.Join(t.TempDir(), "http_sdk_test.wasm")
	cmd := exec.Command(goBin, "build", "-o", wasmPath, ".")
	cmd.Dir = buildDir
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm", "GOPROXY=off", "GOSUMDB=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("WASM build failed: %v\n%s", err, string(out))
	}

	// Instantiate and run inside real skills.Runner (proves vrax_sandbox.http_get links cleanly & HttpGet("") does not panic).
	rn := skills.NewRunner()
	defer rn.Close(context.Background())

	res, err := rn.Execute(context.Background(), types.SkillManifest{
		ID:      "http-sdk-test",
		Command: wasmPath,
		Engine:  "wasm",
		Tier:    types.Tier3Unverified,
	}, map[string]interface{}{})
	if err != nil {
		t.Fatalf("Runner.Execute failed (import mismatch or panic?): %v", err)
	}
	if res.Output != "empty_len=0" {
		t.Fatalf("expected output 'empty_len=0', got %q", res.Output)
	}
}

func TestBuildAndSaveV1Manifest_ParsesFsRoRwAndPreservesTemplate(t *testing.T) {
	t.Run("Shorthand fs_ro/fs_rw maps virtual pools and host paths accurately", func(t *testing.T) {
		dir := t.TempDir()
		wasmPath := filepath.Join(dir, "demo.wasm")

		m := BuildAndSaveV1Manifest(
			dir, "demo", "Demo", "Test skill", wasmPath, "abc123",
			`{"type":"object"}`, "go",
			[]string{"fs_ro:/workspace", "fs_rw:/output", "fs_read:/input", "fs_write:/etc/secret", "network"},
		)

		if m.Tier != types.Tier3Unverified || m.IsOfficial {
			t.Fatalf("expected fail-closed Tier3Unverified non-official, got tier=%d official=%v", m.Tier, m.IsOfficial)
		}
		if m.ManifestV1 == nil || m.ManifestV1.Capabilities == nil || m.ManifestV1.Capabilities.FS == nil {
			t.Fatalf("expected FS capabilities to be populated from fs_ro/fs_rw")
		}
		mounts := m.ManifestV1.Capabilities.FS.Mounts
		if len(mounts) != 4 {
			t.Fatalf("expected 4 parsed mounts, got %d: %+v", len(mounts), mounts)
		}
		if mounts[0].Source != "workspace" || mounts[0].Mode != "ro" {
			t.Errorf("expected /workspace -> source=workspace ro, got %+v", mounts[0])
		}
		if mounts[1].Source != "output" || mounts[1].Mode != "rw" {
			t.Errorf("expected /output -> source=output rw, got %+v", mounts[1])
		}
		if mounts[2].Source != "input" || mounts[2].Mode != "ro" {
			t.Errorf("expected /input -> source=input ro, got %+v", mounts[2])
		}
		if mounts[3].Source != "host" || mounts[3].Mode != "rw" || mounts[3].HostPath != "/etc/secret" {
			t.Errorf("expected arbitrary path to stay source=host (blocked by Shield), got %+v", mounts[3])
		}
		if m.ManifestV1.Capabilities.Network == nil || m.ManifestV1.Capabilities.Network.Mode != "fetch" {
			t.Errorf("expected network mode=fetch, got %+v", m.ManifestV1.Capabilities.Network)
		}
	})

	t.Run("Preserves authored SkillManifestV1 template while clamping limits and enforcing Tier3", func(t *testing.T) {
		dir := t.TempDir()
		wasmPath := filepath.Join(dir, "analyzer.wasm")
		randTrue := true

		tpl := &types.SkillManifestV1{
			Version: "0.4.2",
			Agent:   types.AgentGovernance{Risk: "high", Approval: "always"},
			Routing: &types.RoutingSpec{
				Keywords: []string{"audit", "inspect"},
				Tags:     []string{"security"},
			},
			IO: &types.IOSpec{
				Input:  types.InputSpec{Via: "stdin", Format: "json", Schema: json.RawMessage(`{"type":"object","required":["target"]}`)},
				Output: types.OutputSpec{Via: "file", Format: "json", Path: "/output/audit.json"},
			},
			Capabilities: &types.CapabilitiesSpec{
				FS: &types.FSSpec{
					Mounts: []types.MountSpec{
						{Guest: "/workspace", Source: "workspace", Subpath: "src", Mode: "ro"},
						{Guest: "/output", Source: "output", Mode: "rw"},
					},
				},
				Network: &types.NetworkSpec{
					Mode:           "fetch",
					AllowedSchemes: []string{"https"},
					AllowedHosts:   []string{"127.0.0.1"},
				},
				Clock:  &types.ClockSpec{Monotonic: true},
				Random: &randTrue,
			},
			Limits: &types.LimitsSpec{
				MemoryMB:     4096,   // should be clamped to 128
				MaxRuntimeMS: 999999, // should be clamped to 60000
			},
		}

		m := BuildAndSaveV1ManifestWithTemplate(dir, "analyzer", "Analyzer", "Audit tool", wasmPath, "deadbeef", "", "go", nil, tpl)

		if m.Tier != types.Tier3Unverified || m.IsOfficial {
			t.Fatalf("template must never bypass Tier3Unverified")
		}
		if m.ManifestV1.Limits.MemoryMB != 128 || m.ManifestV1.Limits.MaxRuntimeMS != 60000 {
			t.Fatalf("expected limits clamped to (128, 60000), got (%d, %d)", m.ManifestV1.Limits.MemoryMB, m.ManifestV1.Limits.MaxRuntimeMS)
		}
		if m.ManifestV1.IO.Output.Via != "file" || m.ManifestV1.IO.Output.Path != "/output/audit.json" {
			t.Fatalf("IO output spec was not preserved: %+v", m.ManifestV1.IO.Output)
		}
		if len(m.ManifestV1.Capabilities.FS.Mounts) != 2 || m.ManifestV1.Capabilities.FS.Mounts[0].Subpath != "src" {
			t.Fatalf("FS mounts were not preserved: %+v", m.ManifestV1.Capabilities.FS)
		}
		if len(m.ManifestV1.Capabilities.Network.AllowedHosts) != 1 || m.ManifestV1.Capabilities.Network.AllowedHosts[0] != "127.0.0.1" {
			t.Fatalf("Network allowlist was not preserved: %+v", m.ManifestV1.Capabilities.Network)
		}
		if len(m.Keywords) != 2 || m.Keywords[0] != "audit" {
			t.Fatalf("Routing keywords were not propagated: %+v", m.Keywords)
		}

		// Verify persisted JSON on disk matches
		raw, err := os.ReadFile(filepath.Join(dir, "analyzer.manifest.json"))
		if err != nil {
			t.Fatalf("failed reading persisted manifest.json: %v", err)
		}
		var diskV1 types.SkillManifestV1
		if err := json.Unmarshal(raw, &diskV1); err != nil {
			t.Fatalf("persisted manifest JSON invalid: %v", err)
		}
		if diskV1.Module.SHA256 != "deadbeef" || diskV1.IO.Output.Path != "/output/audit.json" {
			t.Fatalf("persisted manifest mismatch: %+v", diskV1)
		}
	})
}

func TestZigCoder_ConcurrentCompilesUseDistinctRandomizedTempDirs(t *testing.T) {
	database, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer database.Close()
	repo := db.NewSkillRepository(database)

	recordDir := filepath.Join(t.TempDir(), "invocations")
	if err := os.MkdirAll(recordDir, 0700); err != nil {
		t.Fatalf("mkdir recordDir: %v", err)
	}
	var stub string
	if runtime.GOOS == "windows" {
		stub = filepath.Join(t.TempDir(), "record-zig.bat")
		// Write one file per distinct temp build directory to avoid concurrent log-file lock contention on Windows.
		script := "@echo off\r\n" +
			"for %%A in (%*) do set \"LAST=%%~A\"\r\n" +
			"for %%I in (\"%LAST%\") do set \"DIR=%%~dpI\"\r\n" +
			"set \"DIR=%DIR:~0,-1%\"\r\n" +
			"for %%N in (\"%DIR%\") do echo %DIR%> \"" + recordDir + "\\%%~nxN.log\"\r\n" +
			"exit /b 0\r\n"
		if err := os.WriteFile(stub, []byte(script), 0700); err != nil {
			t.Fatalf("write stub: %v", err)
		}
	} else {
		stub = filepath.Join(t.TempDir(), "record-zig.sh")
		script := "#!/bin/sh\nfor last; do :; done\ndir=$(dirname \"$last\")\nbase=$(basename \"$dir\")\nprintf \"%s\\n\" \"$dir\" > \"" + recordDir + "/$base.log\"\nexit 0\n"
		if err := os.WriteFile(stub, []byte(script), 0700); err != nil {
			t.Fatalf("write stub: %v", err)
		}
	}

	coder := &ZigCoder{
		Repo:      repo,
		TM:        &MockZigToolchain{zigBin: stub},
		Runner:    &MockSkillRunner{DryRunFail: false},
		SkillsDir: t.TempDir(),
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- coder.Compile("Same Skill", "Concurrent compile", "", "pub fn main() !void {}", nil)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent compile failed: %v", err)
		}
	}

	entries, err := os.ReadDir(recordDir)
	if err != nil {
		t.Fatalf("read recordDir: %v", err)
	}
	if len(entries) != 4 {
		t.Fatalf("expected 4 distinct temp build dir records, got %d", len(entries))
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(recordDir, entry.Name()))
		if err != nil {
			t.Fatalf("read record %s: %v", entry.Name(), err)
		}
		dir := strings.TrimSpace(string(raw))
		if !strings.Contains(filepath.Base(dir), "vraxter-zig-build-") {
			t.Fatalf("unexpected temp build dir prefix: %s", dir)
		}
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("temp build dir %s was not cleaned up", dir)
		}
	}
}
