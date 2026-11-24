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
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/pkg/types"
)

func TestValidateSkillInputParams_Draft07Rules(t *testing.T) {
	schemaJSON := json.RawMessage(`{
		"type": "object",
		"additionalProperties": false,
		"required": ["target", "severity"],
		"properties": {
			"target": { "type": "string", "minLength": 2, "maxLength": 16, "pattern": "^[a-z0-9-]+$" },
			"severity": { "type": "string", "enum": ["low", "medium", "high", "critical"] },
			"retries": { "type": "integer", "minimum": 0, "maximum": 5 },
			"tags": {
				"type": "array",
				"minItems": 1,
				"maxItems": 3,
				"uniqueItems": true,
				"items": { "type": "string", "minLength": 1 }
			},
			"dry_run": { "type": "boolean" }
		}
	}`)

	manifest := types.SkillManifest{
		ID: "schema-test-skill",
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "schema-test-skill",
			IO: &types.IOSpec{
				Input: types.InputSpec{
					Via:    "stdin",
					Format: "json",
					Schema: schemaJSON,
				},
			},
		},
	}

	// Valid payload
	valid := map[string]interface{}{
		"target":   "node-01",
		"severity": "high",
		"retries":  3,
		"tags":     []string{"soc", "airgap"},
		"dry_run":  true,
	}
	if err := ValidateSkillInputParams(manifest, valid); err != nil {
		t.Fatalf("expected valid params to pass schema validation, got: %v", err)
	}

	cases := []struct {
		name    string
		params  map[string]interface{}
		wantErr string
	}{
		{
			name:    "MissingRequired",
			params:  map[string]interface{}{"target": "node-01"},
			wantErr: `missing required property "severity"`,
		},
		{
			name:    "NilParamsMissingRequired",
			params:  nil,
			wantErr: `missing required property`,
		},
		{
			name:    "AdditionalPropertyRejected",
			params:  map[string]interface{}{"target": "node-01", "severity": "low", "extra": "nope"},
			wantErr: `additional property "extra" is not allowed`,
		},
		{
			name:    "InvalidEnum",
			params:  map[string]interface{}{"target": "node-01", "severity": "extreme"},
			wantErr: `not one of allowed enum values`,
		},
		{
			name:    "PatternMismatch",
			params:  map[string]interface{}{"target": "BAD_TARGET!", "severity": "low"},
			wantErr: `does not match pattern`,
		},
		{
			name:    "NonIntegerForIntegerField",
			params:  map[string]interface{}{"target": "node-01", "severity": "low", "retries": 1.5},
			wantErr: `expected integer`,
		},
		{
			name:    "OutOfRangeMaximum",
			params:  map[string]interface{}{"target": "node-01", "severity": "low", "retries": 10},
			wantErr: `exceeds maximum`,
		},
		{
			name:    "DuplicateArrayItems",
			params:  map[string]interface{}{"target": "node-01", "severity": "low", "tags": []string{"dup", "dup"}},
			wantErr: `duplicates`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSkillInputParams(manifest, tc.params)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateSkillInputParams_IgnoresLegacyDescriptiveString(t *testing.T) {
	manifest := types.SkillManifest{
		ID:           "legacy-desc",
		ParamsSchema: "file: path to analyze, verbose: optional bool",
	}
	if err := ValidateSkillInputParams(manifest, map[string]interface{}{"anything": 1}); err != nil {
		t.Fatalf("legacy human-readable ParamsSchema should not fail JSON Schema validation: %v", err)
	}
}

func TestRunner_EnforcesInputSchemaAndAuditsDenial(t *testing.T) {
	tmpDir := t.TempDir()
	wasmPath := filepath.Join(tmpDir, "schema-echo.wasm")
	// Minimal valid WASI module writing "OK" to stdout
	minimalWASM := []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00,
		0x01, 0x0c, 0x02,
		0x60, 0x04, 0x7f, 0x7f, 0x7f, 0x7f, 0x01, 0x7f,
		0x60, 0x00, 0x00,
		0x02, 0x23, 0x01,
		0x16, 'w', 'a', 's', 'i', '_', 's', 'n', 'a', 'p', 's', 'h', 'o', 't', '_', 'p', 'r', 'e', 'v', 'i', 'e', 'w', '1',
		0x08, 'f', 'd', '_', 'w', 'r', 'i', 't', 'e',
		0x00, 0x00,
		0x03, 0x02, 0x01, 0x01,
		0x05, 0x03, 0x01, 0x00, 0x01,
		0x07, 0x13, 0x02,
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x01,
		0x0a, 0x0f, 0x01,
		0x0d, 0x00,
		0x41, 0x01,
		0x41, 0x00,
		0x41, 0x01,
		0x41, 0x10,
		0x10, 0x00,
		0x1a,
		0x0b,
		0x0b, 0x10, 0x01,
		0x00, 0x41, 0x00, 0x0b,
		0x0a,
		0x08, 0x00, 0x00, 0x00,
		0x02, 0x00, 0x00, 0x00,
		'O', 'K',
	}
	if err := os.WriteFile(wasmPath, minimalWASM, 0600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}

	store, err := db.NewStore(filepath.Join(tmpDir, "audit.db"))
	if err != nil {
		t.Fatalf("db.NewStore: %v", err)
	}
	defer store.Close()

	ledger, err := audit.NewLedger(store, tmpDir, "")
	if err != nil {
		t.Fatalf("audit.NewLedger: %v", err)
	}

	runner := NewRunner()
	defer runner.Close(context.Background())
	runner.SetAuditLedger(ledger)

	manifest := types.SkillManifest{
		ID:      "schema-echo",
		Engine:  "wasm",
		Command: wasmPath,
		Tier:    types.Tier1Official,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "schema-echo",
			Name:            "Schema Echo",
			Version:         "1.0.0",
			Description:     "Schema test",
			Module:          types.ModuleSpec{File: "schema-echo.wasm", ABI: "wasip1"},
			IO: &types.IOSpec{
				Input: types.InputSpec{
					Via:    "stdin",
					Format: "json",
					Schema: json.RawMessage(`{
						"type": "object",
						"required": ["batch_id"],
						"additionalProperties": false,
						"properties": {
							"batch_id": { "type": "string", "minLength": 3 }
						}
					}`),
				},
				Output: types.OutputSpec{Via: "stdout", Format: "text"},
			},
		},
	}

	// 1. Invalid params must fail closed BEFORE WASM execution and audit POLICY_EVALUATED DENY
	_, err = runner.Execute(context.Background(), manifest, map[string]interface{}{"batch_id": "x"})
	if err == nil || !strings.Contains(err.Error(), "io.input.schema") {
		t.Fatalf("expected io.input.schema rejection, got: %v", err)
	}

	entries, err := ledger.ListEntries(audit.ExportFilter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	foundDeny := false
	for _, e := range entries {
		if e.EventType == audit.EventPolicyEvaluated && strings.Contains(e.PayloadJSON, `"check":"wasm_input_schema"`) && strings.Contains(e.PayloadJSON, `"decision":"DENY"`) {
			foundDeny = true
			break
		}
	}
	if !foundDeny {
		t.Fatalf("expected POLICY_EVALUATED DENY for wasm_input_schema in audit ledger, got %d entries", len(entries))
	}

	// 2. Valid params must succeed and execute the WASM binary
	res, err := runner.Execute(context.Background(), manifest, map[string]interface{}{"batch_id": "batch-001"})
	if err != nil {
		t.Fatalf("expected valid params to execute WASM cleanly, got: %v", err)
	}
	if res == nil || strings.TrimSpace(res.Output) != "OK" {
		t.Fatalf("unexpected WASM output: %+v", res)
	}
}
