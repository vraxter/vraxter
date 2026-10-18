// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tests

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/pkg/types"
)

func TestSkillManifestV1_Validation(t *testing.T) {
	tests := []struct {
		name    string
		v1      types.SkillManifestV1
		wantErr bool
	}{
		{
			name: "valid manifest with all capabilities",
			v1: types.SkillManifestV1{
				Schema:          types.ManifestSchemaV1URI,
				ManifestVersion: types.ManifestVersionV1,
				ID:              "custom.tool",
				Name:            "Custom Tool",
				Version:         "1.0.0",
				Description:     "Test tool description",
				Agent: types.AgentGovernance{
					Risk:     "low",
					Approval: "never",
				},
				Module: types.ModuleSpec{
					File: "custom.wasm",
					ABI:  "wasip1",
				},
				IO: &types.IOSpec{
					Input: types.InputSpec{
						Via:    "stdin",
						Format: "json",
					},
					Output: types.OutputSpec{
						Via:    "stdout",
						Format: "json",
					},
				},
				Capabilities: &types.CapabilitiesSpec{
					FS: &types.FSSpec{
						Mounts: []types.MountSpec{
							{Guest: "/input", Source: "input", Mode: "ro"},
							{Guest: "/output", Source: "output", Mode: "rw"},
						},
					},
					Network: &types.NetworkSpec{Mode: "none"},
				},
				Limits: &types.LimitsSpec{
					MemoryMB:     64,
					MaxRuntimeMS: 10000,
				},
			},
			wantErr: false,
		},
		{
			name: "invalid manifest_version",
			v1: types.SkillManifestV1{
				ManifestVersion: "2.0",
				ID:              "custom.tool",
				Name:            "Custom",
				Version:         "1.0.0",
				Description:     "Test",
				Module:          types.ModuleSpec{File: "custom.wasm"},
			},
			wantErr: true,
		},
		{
			name: "invalid id with spaces",
			v1: types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "invalid id",
				Name:            "Custom",
				Version:         "1.0.0",
				Description:     "Test",
				Module:          types.ModuleSpec{File: "custom.wasm"},
			},
			wantErr: true,
		},
		{
			name: "invalid semver",
			v1: types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "custom.tool",
				Name:            "Custom",
				Version:         "not-a-semver",
				Description:     "Test",
				Module:          types.ModuleSpec{File: "custom.wasm"},
			},
			wantErr: true,
		},
		{
			name: "file output missing guest path",
			v1: types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "custom.tool",
				Name:            "Custom",
				Version:         "1.0.0",
				Description:     "Test",
				Module:          types.ModuleSpec{File: "custom.wasm"},
				IO: &types.IOSpec{
					Output: types.OutputSpec{
						Via: "file",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "mount guest path missing leading slash",
			v1: types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "custom.tool",
				Name:            "Custom",
				Version:         "1.0.0",
				Description:     "Test",
				Module:          types.ModuleSpec{File: "custom.wasm"},
				Capabilities: &types.CapabilitiesSpec{
					FS: &types.FSSpec{
						Mounts: []types.MountSpec{
							{Guest: "relative/path", Source: "workspace", Mode: "ro"},
						},
					},
				},
			},
			wantErr: true,
		},
		{
			name: "invalid mount mode",
			v1: types.SkillManifestV1{
				ManifestVersion: types.ManifestVersionV1,
				ID:              "custom.tool",
				Name:            "Custom",
				Version:         "1.0.0",
				Description:     "Test",
				Module:          types.ModuleSpec{File: "custom.wasm"},
				Capabilities: &types.CapabilitiesSpec{
					FS: &types.FSSpec{
						Mounts: []types.MountSpec{
							{Guest: "/data", Source: "workspace", Mode: "execute"},
						},
					},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.v1.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestSkillManifestV1_GovernanceAndApprovals(t *testing.T) {
	manifestNever := types.SkillManifest{
		ID: "never-tool",
		ManifestV1: &types.SkillManifestV1{
			Agent: types.AgentGovernance{Risk: "critical", Approval: "never"},
		},
	}
	if manifestNever.RequiresApproval(true) {
		t.Errorf("expected approval == false for 'never' policy even if global approval is required")
	}

	manifestAlways := types.SkillManifest{
		ID: "always-tool",
		ManifestV1: &types.SkillManifestV1{
			Agent: types.AgentGovernance{Risk: "low", Approval: "always"},
		},
	}
	if !manifestAlways.RequiresApproval(false) {
		t.Errorf("expected approval == true for 'always' policy even if global approval is false")
	}

	manifestOnRiskLow := types.SkillManifest{
		ID: "low-tool",
		ManifestV1: &types.SkillManifestV1{
			Agent: types.AgentGovernance{Risk: "low", Approval: "on_risk"},
		},
	}
	if manifestOnRiskLow.RequiresApproval(false) {
		t.Errorf("expected approval == false for 'low' risk with on_risk approval when global is false")
	}

	manifestOnRiskHigh := types.SkillManifest{
		ID: "high-tool",
		ManifestV1: &types.SkillManifestV1{
			Agent: types.AgentGovernance{Risk: "high", Approval: "on_risk"},
		},
	}
	if !manifestOnRiskHigh.RequiresApproval(false) {
		t.Errorf("expected approval == true for 'high' risk with on_risk approval")
	}
}

func TestSkillManifestV1_DatabasePersistence(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vraxter-manifest-db-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	store, err := db.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repo := db.NewSkillRepository(store)

	sampleV1 := &types.SkillManifestV1{
		Schema:          types.ManifestSchemaV1URI,
		ManifestVersion: types.ManifestVersionV1,
		ID:              "sample.manifest.skill",
		Name:            "Sample Skill",
		Version:         "0.2.5",
		Description:     "Database persistence test",
		Agent: types.AgentGovernance{
			Risk:     "low",
			Approval: "never",
		},
		Module: types.ModuleSpec{
			File:   "sample.wasm",
			SHA256: "0000000000000000000000000000000000000000000000000000000000000000",
			ABI:    "wasip1",
		},
		IO: &types.IOSpec{
			Output: types.OutputSpec{
				Via:  "file",
				Path: "/output/result.json",
			},
		},
		Capabilities: &types.CapabilitiesSpec{
			FS: &types.FSSpec{
				Mounts: []types.MountSpec{
					{Guest: "/output", Source: "output", Mode: "rw"},
				},
			},
		},
		Limits: &types.LimitsSpec{
			MemoryMB:     64,
			MaxRuntimeMS: 15000,
		},
	}

	manifest := sampleV1.ToLegacyManifest()
	if err := repo.UpsertSkill(manifest); err != nil {
		t.Fatalf("failed to upsert skill: %v", err)
	}

	fetched, err := repo.FindSkill("sample.manifest.skill")
	if err != nil {
		t.Fatalf("failed to find skill: %v", err)
	}

	if fetched.ManifestV1 == nil {
		t.Fatalf("expected ManifestV1 to be reconstructed from DB, got nil")
	}

	if fetched.ManifestV1.ManifestVersion != types.ManifestVersionV1 {
		t.Errorf("expected manifest_version '%s', got '%s'", types.ManifestVersionV1, fetched.ManifestV1.ManifestVersion)
	}
	if fetched.ManifestV1.Agent.Approval != "never" {
		t.Errorf("expected agent.approval 'never', got '%s'", fetched.ManifestV1.Agent.Approval)
	}
	if fetched.ManifestV1.IO.Output.Via != "file" {
		t.Errorf("expected io.output.via 'file', got '%s'", fetched.ManifestV1.IO.Output.Via)
	}
	if fetched.GetExecutionTimeout(time.Minute) != 15*time.Second {
		t.Errorf("expected timeout 15s, got %v", fetched.GetExecutionTimeout(time.Minute))
	}
}

func TestSkillManifestV1_ServiceRegister(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vraxter-service-register-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "skills.db")
	store, err := db.NewStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	repo := db.NewSkillRepository(store)
	skillsDir := filepath.Join(tmpDir, "installed_skills")
	svc := services.NewSkillService(repo, skillsDir)

	// Create dummy wasm with valid magic header \x00asm\x01\x00\x00\x00
	wasmPath := filepath.Join(tmpDir, "analyzer.wasm")
	wasmContent := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if err := os.WriteFile(wasmPath, wasmContent, 0755); err != nil {
		t.Fatal(err)
	}

	// Create V1 manifest
	manifestJSON := `{
  "$schema": "https://vraxter.io/schemas/skill-manifest.v1.json",
  "manifest_version": "skill-manifest/1",
  "id": "analyzer",
  "name": "Data Analyzer",
  "version": "1.0.0",
  "description": "Analyzes input data stream.",
  "agent": { "risk": "low", "approval": "never" },
  "module": { "file": "analyzer.wasm", "abi": "wasip1" },
  "io": {
    "input": { "via": "stdin", "format": "json" },
    "output": { "via": "stdout", "format": "json" }
  },
  "capabilities": {
    "fs": {
      "mounts": [
        { "guest": "/input", "source": "input", "mode": "ro" }
      ]
    }
  },
  "limits": { "memory_mb": 32, "max_runtime_ms": 5000 }
}`
	manifestPath := filepath.Join(tmpDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0644); err != nil {
		t.Fatal(err)
	}

	// Register from manifest path
	registered, err := svc.RegisterFromWASM(manifestPath, "")
	if err != nil {
		t.Fatalf("failed to register from manifest path: %v", err)
	}

	if registered.ID != "analyzer" {
		t.Errorf("expected ID 'analyzer', got '%s'", registered.ID)
	}
	if registered.ManifestV1 == nil {
		t.Fatalf("expected ManifestV1 to be set on registered skill")
	}
	if registered.ManifestV1.Agent.Approval != "never" {
		t.Errorf("expected approval 'never', got '%s'", registered.ManifestV1.Agent.Approval)
	}

	// Verify installed .manifest.json exists on disk
	installedManifestFile := filepath.Join(skillsDir, "analyzer.manifest.json")
	if _, err := os.Stat(installedManifestFile); err != nil {
		t.Errorf("expected installed manifest at '%s', but not found: %v", installedManifestFile, err)
	}
}

func TestManifestSchemaFile_SyntacticallyValidJSON(t *testing.T) {
	schemaPath := filepath.Join("..", "schemas", "manifest.schema.json")
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("failed to read schema file: %v", err)
	}

	var schemaObj map[string]interface{}
	if err := json.Unmarshal(data, &schemaObj); err != nil {
		t.Fatalf("manifest.schema.json is not valid JSON: %v", err)
	}

	if schemaObj["$id"] != "https://vraxter.io/schemas/skill-manifest.v1.json" {
		t.Errorf("expected $id 'https://vraxter.io/schemas/skill-manifest.v1.json', got %v", schemaObj["$id"])
	}
}

