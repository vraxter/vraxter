// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/internal/skills/coders"
	"github.com/vraxter/vraxter/internal/telemetry"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

// capturingToolProvider records CompletionRequests and emits either EventTypeNativeToolCall
// or VRX textual tool markers to test Orchestrator native tool calling + VRX fallback.
type capturingToolProvider struct {
	mu           sync.Mutex
	requests     []interfaces.CompletionRequest
	callCount    int
	emitNative   bool
	toolFilePath string
}

func (p *capturingToolProvider) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
	return interfaces.CompletionResponse{Content: "OK"}, nil
}

func (p *capturingToolProvider) StreamGenerate(ctx context.Context, req interfaces.CompletionRequest) (<-chan interfaces.StreamEvent, error) {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.callCount++
	turn := p.callCount
	emitNative := p.emitNative
	filePath := p.toolFilePath
	p.mu.Unlock()

	ch := make(chan interfaces.StreamEvent, 8)
	go func() {
		defer close(ch)
		if turn == 1 {
			if emitNative {
				tc := interfaces.ToolCall{
					ID:        "call_native_01",
					Name:      "vraxter-read-file",
					Arguments: map[string]interface{}{"path": filePath},
				}
				ch <- interfaces.StreamEvent{
					Type:     interfaces.EventTypeNativeToolCall,
					SkillID:  tc.Name,
					ToolCall: &tc,
				}
			} else {
				payload, _ := json.Marshal(map[string]interface{}{
					"skill_id": "vraxter-read-file",
					"params":   map[string]interface{}{"path": filePath},
				})
				ch <- interfaces.StreamEvent{
					Type:    interfaces.EventTypeToken,
					Content: "[VRAX_TOOL] " + string(payload),
				}
			}
			ch <- interfaces.StreamEvent{Type: interfaces.EventTypeDone}
			return
		}
		// Second turn: confirm tool result was injected into conversation history
		foundToolContent := false
		for _, m := range req.Messages {
			if strings.Contains(m.Content, "CLASSIFIED_INTEL_MARKER_777") {
				foundToolContent = true
				break
			}
		}
		if foundToolContent {
			ch <- interfaces.StreamEvent{Type: interfaces.EventTypeToken, Content: "VERIFIED_TOOL_ROUNDTRIP"}
		} else {
			ch <- interfaces.StreamEvent{Type: interfaces.EventTypeToken, Content: "MISSING_TOOL_RESULT"}
		}
		ch <- interfaces.StreamEvent{Type: interfaces.EventTypeDone}
	}()
	return ch, nil
}

func (p *capturingToolProvider) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	return nil, nil
}
func (p *capturingToolProvider) Discover(ctx context.Context) ([]interfaces.ModelMetadata, error) {
	return nil, nil
}
func (p *capturingToolProvider) GetModelDetails(ctx context.Context, modelID string) (map[string]interface{}, error) {
	return nil, nil
}
func (p *capturingToolProvider) CheckHealth(ctx context.Context) error { return nil }

func TestMediaMetaSkills_SeeAndHearConfinedMagicBytesAndAudited(t *testing.T) {
	wsDir := t.TempDir()
	outsideDir := t.TempDir()
	bindTestWorkspace(t, wsDir)

	// Valid PNG header inside workspace
	validPNG := append([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}, make([]byte, 32)...)
	validPNGPath := filepath.Join(wsDir, "valid.png")
	if err := os.WriteFile(validPNGPath, validPNG, 0600); err != nil {
		t.Fatalf("write valid.png: %v", err)
	}

	// Valid WAV header inside workspace
	validWAV := append([]byte{'R', 'I', 'F', 'F', 0x24, 0x00, 0x00, 0x00, 'W', 'A', 'V', 'E'}, make([]byte, 32)...)
	validWAVPath := filepath.Join(wsDir, "valid.wav")
	if err := os.WriteFile(validWAVPath, validWAV, 0600); err != nil {
		t.Fatalf("write valid.wav: %v", err)
	}

	// Spoofed image: .png extension but contains PEM private key text
	spoofedPath := filepath.Join(wsDir, "spoofed.png")
	if err := os.WriteFile(spoofedPath, []byte("-----BEGIN PRIVATE KEY-----\nSECRET_KEY\n"), 0600); err != nil {
		t.Fatalf("write spoofed.png: %v", err)
	}

	// Oversized file (> 10 MB)
	oversizedPath := filepath.Join(wsDir, "huge.png")
	bigBuf := make([]byte, skills.MaxMediaReadBytes+64)
	copy(bigBuf, validPNG)
	if err := os.WriteFile(oversizedPath, bigBuf, 0600); err != nil {
		t.Fatalf("write huge.png: %v", err)
	}

	// File outside workspace
	outsidePNG := filepath.Join(outsideDir, "outside.png")
	if err := os.WriteFile(outsidePNG, validPNG, 0600); err != nil {
		t.Fatalf("write outside.png: %v", err)
	}

	// Direct unit verification on ReadWorkspaceMediaFile
	if _, _, err := skills.ReadWorkspaceMediaFile(outsidePNG, "image"); err == nil {
		t.Fatal("expected outside workspace path to be rejected")
	}
	if _, _, err := skills.ReadWorkspaceMediaFile("../outside.png", "image"); err == nil {
		t.Fatal("expected path traversal to be rejected")
	}
	if _, _, err := skills.ReadWorkspaceMediaFile("~/.vraxter/db_master.key", "image"); err == nil {
		t.Fatal("expected home tilde path to be rejected")
	}
	if _, _, err := skills.ReadWorkspaceMediaFile("spoofed.png", "image"); err == nil || !strings.Contains(err.Error(), "magic-byte") {
		t.Fatalf("expected spoofed.png to fail magic-byte verification, got: %v", err)
	}
	if _, _, err := skills.ReadWorkspaceMediaFile("huge.png", "image"); err == nil || !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Fatalf("expected oversized image to fail size cap, got: %v", err)
	}
	if data, mime, err := skills.ReadWorkspaceMediaFile("valid.png", "image"); err != nil || mime != "image/png" || !bytes.Equal(data, validPNG) {
		t.Fatalf("expected valid.png to succeed as image/png, got mime=%q err=%v", mime, err)
	}
	if data, mime, err := skills.ReadWorkspaceMediaFile("valid.wav", "audio"); err != nil || mime != "audio/wav" || !bytes.Equal(data, validWAV) {
		t.Fatalf("expected valid.wav to succeed as audio/wav, got mime=%q err=%v", mime, err)
	}

	// Verify ExecutionEngine commands vraxter-see & vraxter-hear log DENY & ALLOW to audit_ledger
	engine, store := newTestExecEngine(t)
	ledger, err := audit.NewLedger(store, t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	engine.AuditLedger = ledger

	chatRepo := db.NewChatRepository(store)
	_ = chatRepo.CreateConversation(&types.Conversation{ID: "conv-media", Title: "media"})

	out := make(chan llm.StreamEvent, 16)
	badPayload, _ := json.Marshal(map[string]interface{}{
		"skill_id": "vraxter-see",
		"params":   map[string]interface{}{"path": spoofedPath},
	})
	resBad := engine.DispatchToolPayload(context.Background(), out, string(badPayload), "", "conv-media")
	if !strings.Contains(resBad, "Access denied") {
		t.Fatalf("expected vraxter-see to reject spoofed.png, got: %q", resBad)
	}

	hearOutside, _ := json.Marshal(map[string]interface{}{
		"skill_id": "vraxter-hear",
		"params":   map[string]interface{}{"path": outsidePNG},
	})
	resHear := engine.DispatchToolPayload(context.Background(), out, string(hearOutside), "", "conv-media")
	if !strings.Contains(resHear, "Access denied") {
		t.Fatalf("expected vraxter-hear to reject outside path, got: %q", resHear)
	}

	okSee, _ := json.Marshal(map[string]interface{}{
		"skill_id": "vraxter-see",
		"params":   map[string]interface{}{"path": "valid.png"},
	})
	// Active model config delegate is nil, so file read ALLOW happens before model check
	_ = engine.DispatchToolPayload(context.Background(), out, string(okSee), "", "conv-media")
	close(out)

	entries, err := ledger.ListEntries(audit.ExportFilter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	var sawDeny, sawHearDeny, sawAllow bool
	for _, e := range entries {
		if e.EventType != audit.EventPolicyEvaluated || !strings.Contains(e.PayloadJSON, `"check":"media_file_read"`) {
			continue
		}
		if strings.Contains(e.PayloadJSON, `"tool":"vraxter-see"`) && strings.Contains(e.PayloadJSON, `"decision":"DENY"`) {
			sawDeny = true
		}
		if strings.Contains(e.PayloadJSON, `"tool":"vraxter-hear"`) && strings.Contains(e.PayloadJSON, `"decision":"DENY"`) {
			sawHearDeny = true
		}
		if strings.Contains(e.PayloadJSON, `"tool":"vraxter-see"`) && strings.Contains(e.PayloadJSON, `"decision":"ALLOW"`) && strings.Contains(e.PayloadJSON, `"mime_type":"image/png"`) {
			sawAllow = true
		}
	}
	if !sawDeny || !sawHearDeny || !sawAllow {
		t.Fatalf("missing expected media_file_read audit entries (seeDeny=%v hearDeny=%v seeAllow=%v)", sawDeny, sawHearDeny, sawAllow)
	}
}

func TestExecutionEngine_IsDaemonCannotBypassHITLAndRequiresManifestOptIn(t *testing.T) {
	engine, store := newTestExecEngine(t)
	ledger, err := audit.NewLedger(store, t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	engine.AuditLedger = ledger
	engine.Runner.SetAuditLedger(ledger)
	engine.RequireSkillApproval = true

	// Register two skills: one WITHOUT allow_daemon, one WITH allow_daemon=true & approval=always
	noDaemonSkill := types.SkillManifest{
		ID:      "no-daemon-skill",
		Name:    "No Daemon Skill",
		Version: "1.0.0",
		Engine:  "wasm",
		Command: "nonexistent.wasm",
		Tier:    types.Tier1Official,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "no-daemon-skill",
			Name:            "No Daemon Skill",
			Version:         "1.0.0",
			Description:     "No daemon allowed",
			Agent:           types.AgentGovernance{Risk: "high", Approval: "always", AllowDaemon: false},
			Module:          types.ModuleSpec{File: "nonexistent.wasm", ABI: "wasip1"},
		},
	}
	daemonAllowedSkill := types.SkillManifest{
		ID:      "daemon-allowed-skill",
		Name:    "Daemon Allowed Skill",
		Version: "1.0.0",
		Engine:  "wasm",
		Command: "nonexistent.wasm",
		Tier:    types.Tier1Official,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "daemon-allowed-skill",
			Name:            "Daemon Allowed Skill",
			Version:         "1.0.0",
			Description:     "Daemon allowed only after HITL",
			Agent:           types.AgentGovernance{Risk: "high", Approval: "always", AllowDaemon: true},
			Module:          types.ModuleSpec{File: "nonexistent.wasm", ABI: "wasip1"},
		},
	}
	_ = engine.Registry.Register(noDaemonSkill)
	_ = engine.Registry.Register(daemonAllowedSkill)

	// 1. is_daemon=true on skill without manifest allow_daemon MUST be denied immediately.
	out1 := make(chan llm.StreamEvent, 8)
	res1 := engine.HandleStandardSkill(context.Background(), out1, types.ToolCall{
		SkillID:  "no-daemon-skill",
		IsDaemon: true,
	}, "no-daemon-skill")
	close(out1)
	if !strings.Contains(res1, "not authorized for daemon execution") {
		t.Fatalf("expected daemon denial when agent.allow_daemon=false, got: %q", res1)
	}

	// 2. is_daemon=true on skill WITH allow_daemon=true MUST still yield !YIELD_FOR_APPROVAL (no HITL bypass!).
	ctx, _, span := telemetry.EnsureTraceID(context.Background(), "test.daemon.hitl")
	defer span.End()
	ctx = audit.WithSessionID(ctx, "sess-daemon-hitl")
	ctx = audit.WithAuthenticatedActor(ctx, "mtls:operator-delta")

	out2 := make(chan llm.StreamEvent, 8)
	res2 := engine.HandleStandardSkill(ctx, out2, types.ToolCall{
		SkillID:  "daemon-allowed-skill",
		IsDaemon: true,
	}, "daemon-allowed-skill")
	close(out2)

	if res2 != "!YIELD_FOR_APPROVAL" {
		t.Fatalf("SECURITY FAILURE: is_daemon=true bypassed HITL approval! got: %q", res2)
	}
	sawApprovalEvent := false
	for ev := range out2 {
		if ev.Type == llm.EventTypeSkillApprovalRequest && ev.SkillID == "daemon-allowed-skill" {
			sawApprovalEvent = true
		}
	}
	if !sawApprovalEvent {
		t.Fatal("expected EventTypeSkillApprovalRequest to be emitted before daemon spawn")
	}
	if jobs := engine.JobManager.GetActiveJobs(); len(jobs) != 0 {
		t.Fatalf("expected 0 spawned daemon jobs before HITL approval, got %d", len(jobs))
	}
}

func TestOrchestrator_NativeToolCallingDeterministicParamsAndVRXFallback(t *testing.T) {
	wsDir := t.TempDir()
	bindTestWorkspace(t, wsDir)
	secretNote := filepath.Join(wsDir, "intel.txt")
	if err := os.WriteFile(secretNote, []byte("CLASSIFIED_INTEL_MARKER_777"), 0600); err != nil {
		t.Fatalf("write intel.txt: %v", err)
	}

	for _, tc := range []struct {
		name       string
		emitNative bool
	}{
		{"NativeToolCall", true},
		{"VRXTextFallback", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newTestStore(t)
			cs := newTestCrypto(t)
			reg := skills.NewRegistry()
			runner := skills.NewRunner()
			defer runner.Close(context.Background())

			orch, err := core.NewOrchestrator(store, cs, reg, runner, nil, false, nil, false, "airgap", nil)
			if err != nil {
				t.Fatalf("NewOrchestrator: %v", err)
			}
			orch.SetInferenceDefaults(llm.Float64Ptr(0.0), llm.Int64Ptr(42), 2048)

			mockProv := &capturingToolProvider{
				emitNative:   tc.emitNative,
				toolFilePath: "intel.txt",
			}
			modelCfg := types.ModelConfig{
				ID:            "local-det-model",
				ProviderID:    "prov-local",
				Provider:      "ollama",
				Model:         "llama3-det",
				Alias:         "det-alias",
				Priority:      1,
				IsActive:      true,
				ContextWindow: 8192,
			}
			router := llm.NewRouter([]types.ModelConfig{modelCfg})
			router.Register("prov-local", mockProv)
			orch.StreamCoord = core.NewStreamCoordinator(router, false)

			stream, err := orch.ProcessRawIntent(context.Background(), "sess-"+tc.name, "Analyze the mission briefing and summarize findings", "", "local-det-model", "", "")
			if err != nil {
				t.Fatalf("ProcessRawIntent: %v", err)
			}
			var output strings.Builder
			for ev := range stream {
				if ev.Type == llm.EventTypeToken {
					output.WriteString(ev.Content)
				}
			}

			if !strings.Contains(output.String(), "VERIFIED_TOOL_ROUNDTRIP") {
				t.Fatalf("expected VERIFIED_TOOL_ROUNDTRIP from %s flow, got: %q", tc.name, output.String())
			}

			mockProv.mu.Lock()
			defer mockProv.mu.Unlock()
			if len(mockProv.requests) < 2 {
				t.Fatalf("expected at least 2 LLM turns (tool call + final response), got %d", len(mockProv.requests))
			}
			req0 := mockProv.requests[0]
			if req0.Temperature == nil || *req0.Temperature != 0.0 {
				t.Fatalf("expected deterministic Temperature=0.0, got: %v", req0.Temperature)
			}
			if req0.Seed == nil || *req0.Seed != 42 {
				t.Fatalf("expected deterministic Seed=42, got: %v", req0.Seed)
			}
			if req0.MaxTokens != 2048 {
				t.Fatalf("expected MaxTokens=2048, got: %d", req0.MaxTokens)
			}
			if len(req0.Tools) == 0 {
				t.Fatal("expected CompletionRequest.Tools to be populated with authorized native tool definitions")
			}
			foundReadFile := false
			for _, td := range req0.Tools {
				if td.Name == "vraxter-read-file" {
					foundReadFile = true
					break
				}
			}
			if !foundReadFile {
				t.Fatalf("expected vraxter-read-file in CompletionRequest.Tools, got %+v", req0.Tools)
			}
		})
	}
}

func TestRuntimeCodegenPolicyAndSanitizedGoBuildEnv(t *testing.T) {
	// 1. settings.json with require_signed_skills=true defaults allow_runtime_codegen=false.
	appDir := t.TempDir()
	t.Setenv("VRAXTER_APP_DIR", appDir)
	t.Setenv("VRAXTER_ALLOW_RUNTIME_CODEGEN", "")
	t.Setenv("VRAXTER_REQUIRE_SIGNED_SKILLS", "")
	if err := os.WriteFile(filepath.Join(appDir, "settings.json"), []byte(`{"require_signed_skills": true}`), 0600); err != nil {
		t.Fatalf("write settings.json: %v", err)
	}
	cfg := config.Load()
	if !cfg.RequireSignedSkills {
		t.Fatal("expected RequireSignedSkills=true")
	}
	if cfg.AllowRuntimeCodegen {
		t.Fatal("expected AllowRuntimeCodegen to default to false when require_signed_skills=true")
	}

	// 2. ExecutionEngine blocks vraxter-coder when AllowRuntimeCodegen=false and records audit DENY.
	engine, store := newTestExecEngine(t)
	ledger, err := audit.NewLedger(store, t.TempDir(), "")
	if err != nil {
		t.Fatalf("NewLedger: %v", err)
	}
	engine.AuditLedger = ledger
	engine.AllowRuntimeCodegen = false

	_ = db.NewChatRepository(store).CreateConversation(&types.Conversation{ID: "conv-codegen", Title: "codegen"})
	out := make(chan llm.StreamEvent, 8)
	payload, _ := json.Marshal(map[string]interface{}{
		"skill_id": "vraxter-coder",
		"params":   map[string]interface{}{"name": "rogue-skill", "description": "rogue", "spec": "do stuff"},
	})
	res := engine.DispatchToolPayload(context.Background(), out, string(payload), "", "conv-codegen")
	close(out)
	if !strings.Contains(res, "allow_runtime_codegen=false") {
		t.Fatalf("expected runtime codegen denial, got: %q", res)
	}

	entries, _ := ledger.ListEntries(audit.ExportFilter{})
	foundDeny := false
	for _, e := range entries {
		if e.EventType == audit.EventPolicyEvaluated && strings.Contains(e.PayloadJSON, `"check":"runtime_codegen_policy"`) && strings.Contains(e.PayloadJSON, `"decision":"DENY"`) {
			foundDeny = true
		}
	}
	if !foundDeny {
		t.Fatal("expected runtime_codegen_policy DENY event in audit_ledger")
	}

	// 3. SanitizedGoBuildEnv strips host secrets and pins CGO_ENABLED=0, GOPROXY=off, GOSUMDB=off.
	t.Setenv("OPENAI_API_KEY", "sk-secret-do-not-leak-into-compiler")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "aws-secret-do-not-leak")
	env := coders.SanitizedGoBuildEnv("GOOS=wasip1", "GOARCH=wasm")
	var hasCGO0, hasProxyOff, hasSumOff, leakedSecret bool
	for _, kv := range env {
		switch kv {
		case "CGO_ENABLED=0":
			hasCGO0 = true
		case "GOPROXY=off":
			hasProxyOff = true
		case "GOSUMDB=off":
			hasSumOff = true
		}
		if strings.HasPrefix(kv, "OPENAI_API_KEY=") || strings.HasPrefix(kv, "AWS_SECRET_ACCESS_KEY=") {
			leakedSecret = true
		}
	}
	if !hasCGO0 || !hasProxyOff || !hasSumOff {
		t.Fatalf("expected CGO_ENABLED=0, GOPROXY=off, GOSUMDB=off in sanitized env; got %v", env)
	}
	if leakedSecret {
		t.Fatal("SECURITY FAILURE: host secret leaked into GoCoder build environment")
	}
}

func TestAuditLedger_FailClosedOnCorruptedIdentityKey(t *testing.T) {
	appDir := t.TempDir()
	store := newTestStore(t)
	cs := newTestCrypto(t)

	// Corrupt existing audit_identity.key on disk
	if err := os.WriteFile(filepath.Join(appDir, "audit_identity.key"), []byte("CORRUPTED_NOT_PEM_KEY"), 0600); err != nil {
		t.Fatalf("seed corrupt key: %v", err)
	}

	if _, err := audit.NewLedger(store, appDir, ""); err == nil {
		t.Fatal("expected audit.NewLedger to fail closed on corrupted audit_identity.key")
	}

	reg := skills.NewRegistry()
	runner := skills.NewRunner()
	defer runner.Close(context.Background())
	if _, err := core.NewEngine(store, cs, reg, runner, nil, false, appDir, false, "airgap", nil); err == nil {
		t.Fatal("expected core.NewEngine to fail closed when forensic audit ledger cannot initialize")
	}
}

func TestAirgapStrictLoopbackOnlyVsLocalLAN(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		host      string
		whitelist []string
		wantOK    bool
	}{
		{"airgap loopback IPv4", "airgap", "127.0.0.1", nil, true},
		{"airgap loopback IPv6", "airgap", "::1", nil, true},
		{"airgap localhost", "airgap", "localhost", nil, true},
		{"airgap blocks RFC1918 10.x", "airgap", "10.0.0.5", nil, false},
		{"airgap blocks RFC1918 172.16.x", "airgap", "172.16.5.10", nil, false},
		{"airgap blocks RFC1918 192.168.x", "airgap", "192.168.1.50", nil, false},
		{"airgap blocks .local hostname", "airgap", "gpu-node.local", nil, false},
		{"airgap allows whitelisted RFC1918 IP", "airgap", "10.10.0.20", []string{"10.10.0.20"}, true},
		{"airgap allows whitelisted RFC1918 CIDR", "airgap", "10.10.0.25", []string{"10.10.0.0/24"}, true},
		{"strict_local blocks RFC1918", "strict_local", "192.168.10.5", nil, false},
		{"local_lan allows RFC1918 10.x", "local_lan", "10.0.0.5", nil, true},
		{"local_lan allows RFC1918 172.16.x", "local_lan", "172.16.5.10", nil, true},
		{"local_lan allows RFC1918 192.168.x", "local_lan", "192.168.1.50", nil, true},
		{"local_lan blocks WAN IP", "local_lan", "8.8.8.8", nil, false},
		{"local_lan blocks IMDS", "local_lan", "169.254.169.254", nil, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := utils.IsAllowedHost(tc.host, tc.mode, tc.whitelist)
			if ok != tc.wantOK {
				t.Fatalf("IsAllowedHost(%q, %q, %v) = (%v, %v), want %v", tc.host, tc.mode, tc.whitelist, ok, err, tc.wantOK)
			}
		})
	}

	// Ensure unused import safety for security package if needed in future expansions
	_ = security.ErrHostExecDisabled
	_ = time.Second
}
