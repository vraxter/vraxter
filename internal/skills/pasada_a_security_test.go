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
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/pkg/types"
)

func TestRunner_RejectsUntrustedMountParamsAndHonorsContextSessionDirs(t *testing.T) {
	// Build a tiny WASI skill that reads /input/data.txt and echoes its contents.
	src := `package main

import (
	"encoding/json"
	"os"
)

func main() {
	b, err := os.ReadFile("/input/data.txt")
	out := ""
	if err != nil {
		out = "ERR:" + err.Error()
	} else {
		out = string(b)
	}
	resp := map[string]interface{}{
		"jsonrpc": "2.0",
		"result": map[string]interface{}{
			"status": "completed",
			"output": out,
		},
		"id": "mount-guard",
	}
	_ = json.NewEncoder(os.Stdout).Encode(resp)
}
`
	buildDir := t.TempDir()
	wasmPath, wasmHash := compileTestWASM(t, buildDir, "mount_guard", src)

	secretHostDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretHostDir, "data.txt"), []byte("HOST_SECRET_KEY_MATERIAL"), 0600); err != nil {
		t.Fatalf("seed secret dir: %v", err)
	}
	trustedSessionDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(trustedSessionDir, "data.txt"), []byte("TRUSTED_MISSION_INPUT"), 0600); err != nil {
		t.Fatalf("seed session dir: %v", err)
	}

	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()
	ledger, err := audit.NewLedger(store, t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}

	runner := NewRunner()
	defer runner.Close(context.Background())
	runner.SetAuditLedger(ledger)

	manifest := types.SkillManifest{
		ID:       "mount-guard",
		Name:     "Mount Guard",
		Version:  "1.0.0",
		Engine:   "wasm",
		Command:  wasmPath,
		Checksum: wasmHash,
		Tier:     types.Tier1Official,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "mount-guard",
			Name:            "Mount Guard",
			Version:         "1.0.0",
			Description:     "Verifies mount param rejection",
			Module:          types.ModuleSpec{File: "mount_guard.wasm", SHA256: wasmHash, ABI: "wasip1"},
			Capabilities: &types.CapabilitiesSpec{
				FS: &types.FSSpec{
					Mounts: []types.MountSpec{
						{Guest: "/input", Source: "input", Mode: "ro"},
					},
				},
			},
		},
	}

	// 1. Untrusted LLM params attempting to supply input_dir / _input_dir / output_dir / _output_dir MUST be rejected.
	for _, badKey := range []string{"input_dir", "_input_dir", "output_dir", "_output_dir"} {
		res, err := runner.Execute(context.Background(), manifest, map[string]interface{}{
			badKey: secretHostDir,
		})
		if err == nil {
			t.Fatalf("SECURITY FAILURE: param %q was accepted instead of rejected (output=%+v)", badKey, res)
		}
		if !strings.Contains(err.Error(), "VRAXTER SHIELD") {
			t.Fatalf("expected VRAXTER SHIELD rejection for %q, got: %v", badKey, err)
		}
	}

	// Verify audit ledger recorded the policy denials.
	entries, err := ledger.ListEntries(audit.ExportFilter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	denials := 0
	for _, e := range entries {
		if e.EventType == audit.EventPolicyEvaluated && strings.Contains(e.PayloadJSON, `"check":"wasm_mount_param_guard"`) && strings.Contains(e.PayloadJSON, `"decision":"DENY"`) {
			denials++
		}
	}
	if denials != 4 {
		t.Fatalf("expected 4 wasm_mount_param_guard DENY audit entries, got %d", denials)
	}

	// 2. Host-injected context WithSessionDirs succeeds cleanly and reads only the trusted session dir.
	ctx := WithSessionDirs(context.Background(), SessionDirs{InputDir: trustedSessionDir})
	res, err := runner.Execute(ctx, manifest, map[string]interface{}{})
	if err != nil {
		t.Fatalf("expected trusted WithSessionDirs execution to succeed, got: %v", err)
	}
	if res.Output != "TRUSTED_MISSION_INPUT" {
		t.Fatalf("expected TRUSTED_MISSION_INPUT, got %q", res.Output)
	}
}

func TestRunner_FailClosedOnFuelInstrumentationError(t *testing.T) {
	dir := t.TempDir()
	// Minimal valid WASM module with empty _start export
	src := `package main
import "fmt"
func main() { fmt.Print("{\"jsonrpc\":\"2.0\",\"result\":{\"status\":\"completed\",\"output\":\"OK\"},\"id\":\"fuel-fail\"}") }
`
	wasmPath, wasmHash := compileTestWASM(t, dir, "fuel_fail", src)

	runner := NewRunner()
	defer runner.Close(context.Background())

	injectedErr := errors.New("simulated fuel bytecode parser failure")
	runner.FuelInstrumentFn = func([]byte, int64) ([]byte, error) {
		return nil, injectedErr
	}

	manifest := types.SkillManifest{
		ID:       "fuel-fail",
		Name:     "Fuel Fail",
		Version:  "1.0.0",
		Engine:   "wasm",
		Command:  wasmPath,
		Checksum: wasmHash,
		Tier:     types.Tier1Official,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "fuel-fail",
			Name:            "Fuel Fail",
			Version:         "1.0.0",
			Description:     "Tests fail-closed fuel instrumentation",
			Module:          types.ModuleSpec{File: "fuel_fail.wasm", SHA256: wasmHash, ABI: "wasip1"},
		},
	}

	if _, err := runner.Execute(context.Background(), manifest, nil); err == nil {
		t.Fatal("SECURITY FAILURE: Runner executed un-instrumented WASM binary after fuel instrumentation failed")
	} else if !errors.Is(err, injectedErr) || !strings.Contains(err.Error(), "mandatory WASM CPU fuel instrumentation failed") {
		t.Fatalf("expected fail-closed fuel instrumentation error wrapping injectedErr, got: %v", err)
	}
}
