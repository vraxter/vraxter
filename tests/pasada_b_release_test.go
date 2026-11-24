// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/types"
)

func TestPasadaB_ManifestSchemaSynchronizedWithRuntimeAndDemos(t *testing.T) {
	schemaPath := filepath.Join("..", "schemas", "manifest.schema.json")
	raw, err := os.ReadFile(schemaPath)
	if err != nil {
		t.Fatalf("read manifest.schema.json: %v", err)
	}

	var doc map[string]interface{}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("manifest.schema.json is not valid JSON: %v", err)
	}

	props := doc["properties"].(map[string]interface{})

	// Verify limits.max_cpu_fuel exists
	limitsProps := props["limits"].(map[string]interface{})["properties"].(map[string]interface{})
	if _, ok := limitsProps["max_cpu_fuel"]; !ok {
		t.Fatalf("expected limits.properties.max_cpu_fuel in schemas/manifest.schema.json")
	}

	// Verify agent.allow_daemon and agent.approval enum
	agentProps := props["agent"].(map[string]interface{})["properties"].(map[string]interface{})
	if _, ok := agentProps["allow_daemon"]; !ok {
		t.Fatalf("expected agent.properties.allow_daemon in schemas/manifest.schema.json")
	}
	approvalEnum := agentProps["approval"].(map[string]interface{})["enum"].([]interface{})
	wantApprovals := map[string]bool{"never": false, "always": false, "on_risk": false, "conditional": false}
	for _, v := range approvalEnum {
		if s, ok := v.(string); ok {
			wantApprovals[s] = true
		}
	}
	for k, seen := range wantApprovals {
		if !seen {
			t.Fatalf("expected agent.approval enum to include %q", k)
		}
	}

	// Verify both demo manifests validate against SkillManifestV1
	for _, rel := range []string{
		filepath.Join("..", "demos", "01-aerospace-telemetry-airgap", "manifest.json"),
		filepath.Join("..", "demos", "02-sovereign-soc-triage", "manifest.json"),
	} {
		mb, err := os.ReadFile(rel)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		var m types.SkillManifestV1
		if err := json.Unmarshal(mb, &m); err != nil {
			t.Fatalf("unmarshal %s: %v", rel, err)
		}
		if err := m.Validate(); err != nil {
			t.Fatalf("manifest %s failed Validate(): %v", rel, err)
		}
		if m.Limits == nil || m.Limits.MaxCPUFuel <= 0 {
			t.Fatalf("expected positive limits.max_cpu_fuel in %s", rel)
		}
	}
}

func TestPasadaB_ToolchainChecksumsVerifiedAndUnverifiedFailClosed(t *testing.T) {
	sdkDir := t.TempDir()
	tm := services.NewToolchainManager(sdkDir, false)

	// Verified official Go 1.22.5 checksums must resolve
	verifiedArchives := map[string]string{
		"go1.22.5.linux-amd64.tar.gz":  "904b924d435eaea086515bc63235b192ea441bd8c9b198c507e85009e6e4c7f0",
		"go1.22.5.windows-amd64.zip":   "59968438b8d90f108fd240d4d2f95b037e59716995f7409e0a322dcb996e9f42",
		"zig-linux-x86_64-0.13.0.tar.xz": "d45312e61ebcc48032b77bc4cf7fd6915c11fa16e4aad116b66c9468211230ea",
		"zig-windows-x86_64-0.13.0.zip":  "d859994725ef9402381e557c60bb57497215682e355204d754ee3df75ee3c158",
	}
	for archive, want := range verifiedArchives {
		got, err := tm.ResolveSDKChecksum("go", "https://example.invalid/"+archive)
		if err != nil {
			t.Fatalf("expected verified checksum for %s, got err: %v", archive, err)
		}
		if got != want {
			t.Fatalf("checksum mismatch for %s: got %s, want %s", archive, got, want)
		}
	}

	// Removed unverified/synthetic placeholder archives MUST fail closed
	unverified := []string{
		"https://example.invalid/wasi-sdk-24.0-arm64-linux.tar.gz",
		"https://example.invalid/wasi-sdk-24.0-x86_64-windows.tar.gz",
		"https://example.invalid/zig-windows-aarch64-0.13.0.zip",
		"https://example.invalid/tinygo0.32.0.linux-amd64.tar.gz",
		"https://example.invalid/rust-1.80.0-x86_64-unknown-linux-gnu.tar.gz",
	}
	for _, rawURL := range unverified {
		if sha, err := tm.ResolveSDKChecksum("sdk", rawURL); err == nil {
			t.Fatalf("expected fail-closed missing checksum for unverified %s, got %s", rawURL, sha)
		}
	}
}

func TestPasadaB_AppStoreReputationCannotBypassSignerTrust(t *testing.T) {
	dir := t.TempDir()
	wasmPath := filepath.Join(dir, "high-rep-untrusted.wasm")
	if err := os.WriteFile(wasmPath, minimalValidWASIModule(), 0600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}

	runner := skills.NewRunner()
	defer runner.Close(context.Background())

	// Even with fabricated Score=5.0 and Downloads=999999, Tier3Unverified privileged skill MUST be denied.
	manifest := types.SkillManifest{
		ID:          "high-rep-untrusted",
		Engine:      "wasm",
		Command:     wasmPath,
		Tier:        types.Tier3Unverified,
		Score:       5.0,
		Downloads:   999999,
		Permissions: []string{"network"},
	}
	_, err := runner.Execute(context.Background(), manifest, nil)
	if err == nil || !strings.Contains(err.Error(), "lacks cryptographic signer trust") {
		t.Fatalf("expected high-reputation unsigned privileged skill to fail closed, got: %v", err)
	}
}

func TestPasadaB_IndependentAuditJSONLVerifyWithPinnedPublicKey(t *testing.T) {
	enclaveDir := t.TempDir()
	store, err := db.NewStore(filepath.Join(enclaveDir, "vraxter.db"))
	if err != nil {
		t.Fatalf("db.NewStore: %v", err)
	}
	defer store.Close()

	ledger, err := audit.NewLedger(store, enclaveDir, "")
	if err != nil {
		t.Fatalf("audit.NewLedger: %v", err)
	}
	if _, err := ledger.Append(context.Background(), audit.AuditEventInput{
		EventType: audit.EventRequestReceived,
		Actor:     "principal:mtls-auditor",
		Payload:   map[string]string{"action": "enclave-check"},
	}); err != nil {
		t.Fatalf("ledger.Append: %v", err)
	}

	jsonlPath := filepath.Join(enclaveDir, "export.jsonl")
	if _, err := ledger.ExportJSONL(jsonlPath, audit.ExportFilter{}); err != nil {
		t.Fatalf("ExportJSONL: %v", err)
	}

	// Verify with enclave's audit_identity.pub
	pubPath := filepath.Join(enclaveDir, "audit_identity.pub")
	pubKey, _, err := security.LoadEd25519PublicKey(pubPath)
	if err != nil {
		t.Fatalf("LoadEd25519PublicKey: %v", err)
	}
	count, err := audit.VerifyJSONLFile(jsonlPath, pubKey)
	if err != nil || count != 1 {
		t.Fatalf("VerifyJSONLFile with matching pinned pubkey failed: count=%d err=%v", count, err)
	}

	// Verify fails closed with a different pinned Ed25519 public key (even though JSONL header embeds enclave pubkey)
	wrongKP, err := security.GenerateEd25519KeyPair("rogue-auditor")
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair: %v", err)
	}
	if _, err := audit.VerifyJSONLFile(jsonlPath, wrongKP.PublicKey); err == nil {
		t.Fatalf("expected VerifyJSONLFile to fail closed when pinned pubkey does not match entry signatures")
	}
}
