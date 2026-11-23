// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package audit_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/internal/telemetry"
	"github.com/vraxter/vraxter/pkg/types"
)

func TestAuditLedger_CausalChainAndVerifyChain(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "vraxter.db")
	liveJSONLPath := filepath.Join(tmpDir, "audit_live.jsonl")
	exportJSONLPath := filepath.Join(tmpDir, "audit_export.jsonl")

	store, err := db.NewStore(dbPath)
	if err != nil {
		t.Fatalf("db.NewStore failed: %v", err)
	}
	defer store.Close()

	ledger, err := audit.NewLedger(store, tmpDir, liveJSONLPath)
	if err != nil {
		t.Fatalf("audit.NewLedger failed: %v", err)
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(filepath.Join(tmpDir, "audit_identity.key"))
		if err != nil {
			t.Fatalf("stat audit_identity.key: %v", err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Fatalf("expected audit_identity.key mode 0600, got %o", fi.Mode().Perm())
		}
	}

	ctx, traceID, rootSpan := telemetry.EnsureTraceID(context.Background(), "test.CausalTurn")
	ctx = audit.WithSessionID(ctx, "session-alpha")
	ctx = audit.WithActor(ctx, "user")

	causalSequence := []struct {
		eventType string
		actor     string
		payload   map[string]interface{}
	}{
		{
			eventType: audit.EventRequestReceived,
			actor:     "user",
			payload: map[string]interface{}{
				"input_sha256":   "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				"origin":         "grpc",
				"isolation_mode": "airgap",
			},
		},
		{
			eventType: audit.EventContextAssembled,
			actor:     "system",
			payload: map[string]interface{}{
				"sources":      []string{"models_catalog", "rag_memories"},
				"memory_count": 2,
			},
		},
		{
			eventType: audit.EventPolicyEvaluated,
			actor:     "system",
			payload: map[string]interface{}{
				"check":      "wasm_trust_and_signature",
				"skill_id":   "aero.telemetry_ingest",
				"trust_tier": 1,
				"decision":   "ALLOW",
			},
		},
		{
			eventType: audit.EventHITLDecision,
			actor:     "user",
			payload: map[string]interface{}{
				"decision":    "APPROVED",
				"target_type": "skill",
				"skill_id":    "aero.telemetry_ingest",
			},
		},
		{
			eventType: audit.EventWASMExecStart,
			actor:     "specialist:Telemetry_Engineer",
			payload: map[string]interface{}{
				"skill_id":      "aero.telemetry_ingest",
				"wasm_sha256":   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				"trust_tier":    1,
				"signer_key_id": ledger.SignerKeyID(),
			},
		},
		{
			eventType: audit.EventWASMExecEnd,
			actor:     "specialist:Telemetry_Engineer",
			payload: map[string]interface{}{
				"skill_id":      "aero.telemetry_ingest",
				"wasm_sha256":   "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
				"trust_tier":    1,
				"signer_key_id": ledger.SignerKeyID(),
				"exit_status":   "completed",
				"duration_ms":   14,
			},
		},
		{
			eventType: audit.EventResponseEmitted,
			actor:     "system",
			payload: map[string]interface{}{
				"model_id":         "qwen2.5-coder:14b",
				"status":           "completed",
				"total_latency_ms": 42,
			},
		},
	}

	var prevHash = audit.GenesisPrevHash
	for i, step := range causalSequence {
		entry, err := ledger.Append(ctx, audit.AuditEventInput{
			Actor:     step.actor,
			EventType: step.eventType,
			Payload:   step.payload,
		})
		if err != nil {
			t.Fatalf("Append step %d (%s) failed: %v", i+1, step.eventType, err)
		}
		if entry.Seq != int64(i+1) {
			t.Fatalf("expected seq=%d, got %d", i+1, entry.Seq)
		}
		if entry.TraceID != traceID {
			t.Fatalf("expected propagated trace_id %s, got %s", traceID, entry.TraceID)
		}
		if entry.PrevHash != prevHash {
			t.Fatalf("expected prev_hash %s at seq=%d, got %s", prevHash, entry.Seq, entry.PrevHash)
		}
		prevHash = entry.EntryHash
	}
	rootSpan.End()

	// Verify SQLite chain
	verifiedCount, err := ledger.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain failed on valid chain: %v", err)
	}
	if verifiedCount != len(causalSequence) {
		t.Fatalf("expected %d verified entries, got %d", len(causalSequence), verifiedCount)
	}

	// Verify live JSONL sink
	jsonlCount, err := audit.VerifyJSONLFile(liveJSONLPath, ledger.PublicKey())
	if err != nil {
		t.Fatalf("VerifyJSONLFile(live) failed: %v", err)
	}
	if jsonlCount != len(causalSequence) {
		t.Fatalf("expected %d live JSONL entries, got %d", len(causalSequence), jsonlCount)
	}

	// Verify exported JSONL file (with embedded daemon_public_key header, passing nil pubKey)
	exportedCount, err := ledger.ExportJSONL(exportJSONLPath, audit.ExportFilter{})
	if err != nil {
		t.Fatalf("ExportJSONL failed: %v", err)
	}
	if exportedCount != len(causalSequence) {
		t.Fatalf("expected %d exported entries, got %d", len(causalSequence), exportedCount)
	}
	verifiedExportCount, err := audit.VerifyJSONLFile(exportJSONLPath, nil)
	if err != nil {
		t.Fatalf("VerifyJSONLFile(export) with embedded public key failed: %v", err)
	}
	if verifiedExportCount != len(causalSequence) {
		t.Fatalf("expected %d verified exported entries, got %d", len(causalSequence), verifiedExportCount)
	}
}

func TestAuditLedger_SQLiteTriggersBlockUpdateAndDelete(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := db.NewStore(filepath.Join(tmpDir, "vraxter.db"))
	if err != nil {
		t.Fatalf("db.NewStore failed: %v", err)
	}
	defer store.Close()

	ledger, err := audit.NewLedger(store, tmpDir, "")
	if err != nil {
		t.Fatalf("audit.NewLedger failed: %v", err)
	}

	if _, err := ledger.Append(context.Background(), audit.AuditEventInput{
		Actor:     "user",
		EventType: audit.EventRequestReceived,
		Payload:   map[string]string{"action": "ping"},
	}); err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	// Direct UPDATE must be blocked by BEFORE UPDATE trigger
	_, updateErr := store.Conn.Exec(`UPDATE audit_ledger SET payload_json = '{"action":"tampered"}' WHERE seq = 1`)
	if updateErr == nil || !strings.Contains(updateErr.Error(), "audit_ledger is append-only") {
		t.Fatalf("expected SQLite BEFORE UPDATE trigger to abort with 'audit_ledger is append-only', got: %v", updateErr)
	}

	// Direct DELETE must be blocked by BEFORE DELETE trigger
	_, deleteErr := store.Conn.Exec(`DELETE FROM audit_ledger WHERE seq = 1`)
	if deleteErr == nil || !strings.Contains(deleteErr.Error(), "audit_ledger is append-only") {
		t.Fatalf("expected SQLite BEFORE DELETE trigger to abort with 'audit_ledger is append-only', got: %v", deleteErr)
	}
}

func TestAuditLedger_VerifyChainDetectsPayloadTamperingAndDeletedRow(t *testing.T) {
	// Subtest 1: Tampering with payload_json at seq=2 is caught immediately at seq=2
	t.Run("DetectsPayloadTamperingAtExactSeq", func(t *testing.T) {
		tmpDir := t.TempDir()
		store, err := db.NewStore(filepath.Join(tmpDir, "vraxter.db"))
		if err != nil {
			t.Fatalf("db.NewStore failed: %v", err)
		}
		defer store.Close()

		ledger, err := audit.NewLedger(store, tmpDir, "")
		if err != nil {
			t.Fatalf("audit.NewLedger failed: %v", err)
		}

		for _, ev := range []string{audit.EventRequestReceived, audit.EventWASMExecEnd, audit.EventResponseEmitted} {
			if _, err := ledger.Append(context.Background(), audit.AuditEventInput{
				Actor:     "system",
				EventType: ev,
				Payload:   map[string]interface{}{"step": ev, "status": "ok"},
			}); err != nil {
				t.Fatalf("Append(%s) failed: %v", ev, err)
			}
		}

		// Simulate attacker with raw DB access dropping the trigger and mutating seq=2 payload_json
		if _, err := store.Conn.Exec(`DROP TRIGGER trg_audit_ledger_no_update`); err != nil {
			t.Fatalf("drop trigger: %v", err)
		}
		if _, err := store.Conn.Exec(`UPDATE audit_ledger SET payload_json = '{"step":"WASM_EXEC_END","status":"hacked"}' WHERE seq = 2`); err != nil {
			t.Fatalf("tamper update: %v", err)
		}

		_, verifyErr := ledger.VerifyChain()
		if verifyErr == nil {
			t.Fatalf("expected VerifyChain to fail after payload_json tampering")
		}
		var vErr *audit.VerificationError
		if !errors.As(verifyErr, &vErr) {
			t.Fatalf("expected *audit.VerificationError, got %T: %v", verifyErr, verifyErr)
		}
		if vErr.Seq != 2 {
			t.Fatalf("expected compromised seq=2 to be reported, got seq=%d (%v)", vErr.Seq, vErr)
		}
	})

	// Subtest 2: Deleting an intermediate row (seq=2) is caught immediately at seq=2
	t.Run("DetectsDeletedIntermediateRow", func(t *testing.T) {
		tmpDir := t.TempDir()
		store, err := db.NewStore(filepath.Join(tmpDir, "vraxter.db"))
		if err != nil {
			t.Fatalf("db.NewStore failed: %v", err)
		}
		defer store.Close()

		ledger, err := audit.NewLedger(store, tmpDir, "")
		if err != nil {
			t.Fatalf("audit.NewLedger failed: %v", err)
		}

		for _, ev := range []string{audit.EventRequestReceived, audit.EventWASMExecEnd, audit.EventResponseEmitted} {
			if _, err := ledger.Append(context.Background(), audit.AuditEventInput{
				Actor:     "system",
				EventType: ev,
				Payload:   map[string]interface{}{"step": ev},
			}); err != nil {
				t.Fatalf("Append(%s) failed: %v", ev, err)
			}
		}

		// Simulate attacker dropping the delete trigger and removing intermediate row seq=2
		if _, err := store.Conn.Exec(`DROP TRIGGER trg_audit_ledger_no_delete`); err != nil {
			t.Fatalf("drop trigger: %v", err)
		}
		if _, err := store.Conn.Exec(`DELETE FROM audit_ledger WHERE seq = 2`); err != nil {
			t.Fatalf("tamper delete: %v", err)
		}

		_, verifyErr := ledger.VerifyChain()
		if verifyErr == nil {
			t.Fatalf("expected VerifyChain to fail when intermediate row seq=2 is deleted")
		}
		var vErr *audit.VerificationError
		if !errors.As(verifyErr, &vErr) {
			t.Fatalf("expected *audit.VerificationError, got %T: %v", verifyErr, verifyErr)
		}
		if vErr.Seq != 2 {
			t.Fatalf("expected missing seq=2 to be reported, got seq=%d (%v)", vErr.Seq, vErr)
		}
	})
}

func TestAuditLedger_AutomaticSecretRedaction(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := db.NewStore(filepath.Join(tmpDir, "vraxter.db"))
	if err != nil {
		t.Fatalf("db.NewStore failed: %v", err)
	}
	defer store.Close()

	ledger, err := audit.NewLedger(store, tmpDir, "")
	if err != nil {
		t.Fatalf("audit.NewLedger failed: %v", err)
	}

	rawOpenAIKey := "sk-proj-1234567890abcdefghijklmnop"
	rawGeminiKey := "AIzaSyA1234567890abcdefghijklmnopqrstuv"
	rawBearer := "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"

	entry, err := ledger.Append(context.Background(), audit.AuditEventInput{
		Actor:     "user",
		EventType: audit.EventRequestReceived,
		Payload: map[string]interface{}{
			"api_key":        rawOpenAIKey,
			"client_secret":  "my-top-secret-value",
			"password":       "hunter2-password",
			"authorization":  rawBearer,
			"inline_leak":    "connecting with key " + rawGeminiKey + " and " + rawOpenAIKey,
			"signer_key_id":  "0123456789abcdef",
			"tokens_used":    128,
			"isolation_mode": "airgap",
		},
	})
	if err != nil {
		t.Fatalf("Append failed: %v", err)
	}

	for _, secret := range []string{rawOpenAIKey, rawGeminiKey, "my-top-secret-value", "hunter2-password", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9"} {
		if strings.Contains(entry.PayloadJSON, secret) {
			t.Fatalf("secret %q leaked into persisted payload_json: %s", secret, entry.PayloadJSON)
		}
	}
	if !strings.Contains(entry.PayloadJSON, audit.RedactedPlaceholder) {
		t.Fatalf("expected %s in payload_json, got: %s", audit.RedactedPlaceholder, entry.PayloadJSON)
	}
	// Non-sensitive fields must remain intact
	if !strings.Contains(entry.PayloadJSON, `"signer_key_id":"0123456789abcdef"`) {
		t.Fatalf("expected signer_key_id to remain unredacted in payload_json: %s", entry.PayloadJSON)
	}
	if !strings.Contains(entry.PayloadJSON, `"tokens_used":128`) {
		t.Fatalf("expected tokens_used to remain unredacted in payload_json: %s", entry.PayloadJSON)
	}
}

func TestTelemetry_ZeroEgressInAirgapAndTracePropagation(t *testing.T) {
	tmpDir := t.TempDir()
	spansJSONL := filepath.Join(tmpDir, "spans.jsonl")

	// Attempting to configure a remote OTLP endpoint in airgap or local_lan must be blocked with zero network egress
	for _, restrictedMode := range []string{"airgap", "local_lan"} {
		provider, err := telemetry.Configure(telemetry.Config{
			IsolationMode:  restrictedMode,
			JSONLPath:      spansJSONL,
			RemoteEndpoint: "https://otel-collector.evil.example.com:4318/v1/traces",
		})
		if err == nil {
			t.Fatalf("expected Configure to reject remote OTel endpoint in %s mode", restrictedMode)
		}
		if provider.NetworkEgressCount() != 0 {
			t.Fatalf("expected 0 network egress in %s mode, got %d", restrictedMode, provider.NetworkEgressCount())
		}
		if provider.BlockedEgressTarget() == "" {
			t.Fatalf("expected BlockedEgressTarget to record blocked remote endpoint in %s mode", restrictedMode)
		}
	}

	// Configure valid local-only airgap tracer provider and verify trace_id propagation across spans
	provider, err := telemetry.Configure(telemetry.Config{
		IsolationMode: "airgap",
		JSONLPath:     spansJSONL,
	})
	if err != nil {
		t.Fatalf("Configure local airgap telemetry failed: %v", err)
	}

	ctx, rootTraceID, serverSpan := telemetry.EnsureTraceID(context.Background(), "server.Execute")
	ctx, orchSpan := telemetry.StartSpan(ctx, "orchestrator.ProcessRawIntent")
	ctx, wasmSpan := telemetry.StartSpan(ctx, "runner.ExecuteWASM")

	if got := telemetry.TraceIDFromContext(ctx); got != rootTraceID || len(got) != 32 {
		t.Fatalf("expected 32-hex-char trace_id %q propagated to WASM runner, got %q", rootTraceID, got)
	}

	wasmSpan.End()
	orchSpan.End()
	serverSpan.End()

	if provider.NetworkEgressCount() != 0 {
		t.Fatalf("expected 0 network egress after span export, got %d", provider.NetworkEgressCount())
	}

	spans := provider.RecordedSpans()
	if len(spans) != 3 {
		t.Fatalf("expected 3 recorded local spans, got %d", len(spans))
	}
	for _, sp := range spans {
		if sp.TraceID != rootTraceID {
			t.Fatalf("span %s has mismatched trace_id %s (expected %s)", sp.Name, sp.TraceID, rootTraceID)
		}
	}
}

func TestAuditLedger_EndToEndRuntimeOrchestratorAndWASMRunner(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := db.NewStore(filepath.Join(tmpDir, "vraxter.db"))
	if err != nil {
		t.Fatalf("db.NewStore failed: %v", err)
	}
	defer store.Close()

	cryptoSvc, err := security.NewCryptoService(filepath.Join(tmpDir, ".masterkey"))
	if err != nil {
		t.Fatalf("NewCryptoService failed: %v", err)
	}

	skillsDir := filepath.Join(tmpDir, "skills")
	_ = os.MkdirAll(skillsDir, 0700)

	reg := skills.NewRegistry()
	runner := skills.NewRunner()
	skillRepo := db.NewSkillRepository(store)
	skillSvc := services.NewSkillService(skillRepo, skillsDir)

	// Minimal valid WASI module writing "OK" to stdout
	wasmBytes := []byte{
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
		0x41, 0x01, 0x41, 0x00, 0x41, 0x01, 0x41, 0x10, 0x10, 0x00, 0x1a, 0x0b,
		0x0b, 0x10, 0x01,
		0x00, 0x41, 0x00, 0x0b,
		0x0a, 0x08, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00, 'O', 'K',
	}
	wasmPath := filepath.Join(tmpDir, "audit-probe.wasm")
	if err := os.WriteFile(wasmPath, wasmBytes, 0600); err != nil {
		t.Fatalf("write wasm: %v", err)
	}

	manifest, err := skillSvc.RegisterFromWASM(wasmPath, "")
	if err != nil {
		t.Fatalf("RegisterFromWASM failed: %v", err)
	}
	_ = reg.Register(manifest)

	orch, err := core.NewOrchestrator(
		store, cryptoSvc, reg, runner, nil, nil, false, nil, false, "airgap", nil,
	)
	if err != nil {
		t.Fatalf("NewOrchestrator failed: %v", err)
	}

	ledger, err := audit.NewLedger(store, tmpDir, "")
	if err != nil {
		t.Fatalf("NewLedger failed: %v", err)
	}
	orch.SetAuditLedger(ledger)

	// Invoke fast-path skill execution (!audit-probe) through Orchestrator.ProcessRawIntent
	stream, err := orch.ProcessRawIntent(context.Background(), "conv-e2e", "!audit-probe", "", "", "", "operator")
	if err != nil {
		t.Fatalf("ProcessRawIntent failed: %v", err)
	}
	for range stream {
	}

	count, err := ledger.VerifyChain()
	if err != nil {
		t.Fatalf("VerifyChain after runtime execution failed: %v", err)
	}
	if count < 5 {
		t.Fatalf("expected at least 5 causal audit events (REQUEST_RECEIVED, POLICY_EVALUATED, WASM_EXEC_START, WASM_EXEC_END, RESPONSE_EMITTED), got %d", count)
	}

	entries, err := ledger.ListEntries(audit.ExportFilter{})
	if err != nil {
		t.Fatalf("ListEntries failed: %v", err)
	}
	seen := make(map[string]bool)
	firstTraceID := entries[0].TraceID
	for _, e := range entries {
		seen[e.EventType] = true
		if e.TraceID != firstTraceID {
			t.Fatalf("expected unified trace_id %s across causal chain, got %s on %s", firstTraceID, e.TraceID, e.EventType)
		}
	}
	for _, reqType := range []string{
		audit.EventRequestReceived,
		audit.EventPolicyEvaluated,
		audit.EventWASMExecStart,
		audit.EventWASMExecEnd,
		audit.EventResponseEmitted,
	} {
		if !seen[reqType] {
			t.Fatalf("expected causal event %s in audit_ledger, seen=%v", reqType, seen)
		}
	}
	// Ensure types package is referenced cleanly
	if manifest.Tier != types.Tier3Unverified {
		t.Fatalf("expected unsigned probe skill to run as Tier3Unverified, got %d", manifest.Tier)
	}
}
