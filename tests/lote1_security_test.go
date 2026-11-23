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
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/types"
)

func TestLote1_ConfigHostExecDisabledByDefault(t *testing.T) {
	t.Setenv("VRAXTER_APP_DIR", t.TempDir())
	cfg := config.Load()
	if cfg.HostExec.Enabled {
		t.Fatal("expected cfg.HostExec.Enabled to default to false")
	}
	if !strings.HasSuffix(cfg.HostExec.CommandsFile, "host_commands.json") {
		t.Fatalf("expected default commands file to end with host_commands.json, got %q", cfg.HostExec.CommandsFile)
	}
}

func TestLote1_ExternalSkillsAlwaysDefaultToTier3Unverified(t *testing.T) {
	// 1. SkillManifestV1.ToLegacyManifest defaults to Tier3Unverified
	v1 := &types.SkillManifestV1{
		ManifestVersion: types.ManifestVersionV1,
		ID:              "ext.tool",
		Name:            "External Tool",
		Version:         "1.0.0",
		Description:     "External skill",
		Module:          types.ModuleSpec{File: "ext.wasm"},
	}
	if legacy := v1.ToLegacyManifest(); legacy.Tier != types.Tier3Unverified {
		t.Fatalf("expected ToLegacyManifest Tier=%d (Tier3Unverified), got %d", types.Tier3Unverified, legacy.Tier)
	}

	// 2. SkillManifestV1.Validate rejects source:"host", subpath traversal, and io.output.path traversal
	badSubpath := *v1
	badSubpath.Capabilities = &types.CapabilitiesSpec{
		FS: &types.FSSpec{
			Mounts: []types.MountSpec{{Guest: "/data", Source: "workspace", Subpath: "../secrets", Mode: "ro"}},
		},
	}
	if err := badSubpath.Validate(); err == nil {
		t.Fatal("expected Validate() to reject mount.Subpath '../secrets'")
	}

	badHost := *v1
	badHost.Capabilities = &types.CapabilitiesSpec{
		FS: &types.FSSpec{
			Mounts: []types.MountSpec{{Guest: "/host", Source: "host", HostPath: "/etc", Mode: "ro"}},
		},
	}
	if err := badHost.Validate(); err == nil {
		t.Fatal("expected Validate() to reject source:'host' by default")
	}

	badOutPath := *v1
	badOutPath.IO = &types.IOSpec{
		Output: types.OutputSpec{Via: "file", Path: "/output/../../etc/passwd"},
	}
	if err := badOutPath.Validate(); err == nil {
		t.Fatal("expected Validate() to reject io.output.path traversal")
	}

	// 3. SkillService.RegisterFromWASM enforces Tier3Unverified even if manifest JSON tries to claim Tier 1
	tmpDir := t.TempDir()
	store := newTestStore(t)
	skillsDir := filepath.Join(tmpDir, "installed_skills")
	svc := services.NewSkillService(db.NewSkillRepository(store), skillsDir)

	srcDir := filepath.Join(tmpDir, "drop")
	_ = os.MkdirAll(srcDir, 0700)
	wasmPath := filepath.Join(srcDir, "evil.wasm")
	_ = os.WriteFile(wasmPath, []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}, 0755)
	_ = os.WriteFile(filepath.Join(srcDir, "manifest.json"), []byte(`{"id":"evil","name":"Evil","tier":1,"permissions":["network"]}`), 0644)

	registered, err := svc.RegisterFromWASM(wasmPath, "")
	if err != nil {
		t.Fatalf("RegisterFromWASM failed: %v", err)
	}
	if registered.Tier != types.Tier3Unverified {
		t.Fatalf("expected injected skill to be forced to Tier3Unverified (%d), got %d", types.Tier3Unverified, registered.Tier)
	}
	if registered.IsOfficial {
		t.Fatal("injected skill must never have IsOfficial=true")
	}
	if os.PathSeparator == '/' {
		if info, err := os.Stat(filepath.Join(skillsDir, "evil.wasm")); err == nil && info.Mode().Perm() != 0600 {
			t.Errorf("expected installed .wasm permissions 0600, got %o", info.Mode().Perm())
		}
	}
}

func TestLote1_ExecutionEngine_HostExecHITLAndInjectionDenial(t *testing.T) {
	dir := t.TempDir()

	// Build a tiny helper executable to allowlist as ping (read_only) and nmap (elevated)
	srcDir := filepath.Join(dir, "helper")
	_ = os.MkdirAll(srcDir, 0700)
	_ = os.WriteFile(filepath.Join(srcDir, "go.mod"), []byte("module helperprobe\n\ngo 1.24\n"), 0600)
	_ = os.WriteFile(filepath.Join(srcDir, "main.go"), []byte(`package main
import (
	"fmt"
	"os"
	"strings"
)
func main() { fmt.Print("EXECUTED:" + strings.Join(os.Args[1:], ",")) }
`), 0600)
	binName := "probe"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	binPath := filepath.Join(dir, binName)
	buildCmd := exec.Command("go", "build", "-o", binPath, ".")
	buildCmd.Dir = srcDir
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build helper: %v\n%s", err, string(out))
	}
	binHash, err := security.ComputeFileSHA256(binPath)
	if err != nil {
		t.Fatalf("hash helper: %v", err)
	}

	manifestPath := filepath.Join(dir, "host_commands.json")
	manifestData, _ := json.Marshal(security.HostCommandsManifest{
		Commands: map[string]security.HostCommandSpec{
			"ping": {Path: binPath, SHA256: binHash, Tier: security.HostCommandTierReadOnly},
			"nmap": {Path: binPath, SHA256: binHash, Tier: security.HostCommandTierElevated},
		},
	})
	_ = os.WriteFile(manifestPath, manifestData, 0600)

	engine, store := newTestExecEngine(t)
	chatRepo := db.NewChatRepository(store)
	convID := "conv-host-exec"
	chatRepo.CreateConversation(&types.Conversation{ID: convID, Title: "host exec security"})

	// 1. By default HostExecGuard is disabled -> RunFastPath MUST deny "$"
	outDisabled := make(chan llm.StreamEvent, 8)
	engine.RunFastPath(context.Background(), outDisabled, types.IntentMatch{
		Type: types.IntentTypeCommand,
		ID:   "ping",
		Args: []string{"127.0.0.1"},
	}, convID)
	close(outDisabled)
	var disabledTokens string
	for ev := range outDisabled {
		disabledTokens += ev.Content
	}
	if !strings.Contains(disabledTokens, "disabled by default") {
		t.Fatalf("expected disabled denial, got: %q", disabledTokens)
	}

	// Enable guard with allowlist in airgap mode
	engine.HostExecGuard = security.NewHostExecGuard(security.HostExecOptions{
		Enabled:       true,
		CommandsFile:  manifestPath,
		IsolationMode: "airgap",
	})

	// 2. Human read_only command to local target executes
	outRO := make(chan llm.StreamEvent, 8)
	engine.RunFastPath(context.Background(), outRO, types.IntentMatch{
		Type: types.IntentTypeCommand,
		ID:   "ping",
		Args: []string{"127.0.0.1"},
	}, convID)
	close(outRO)
	var roTokens string
	for ev := range outRO {
		if ev.Type == llm.EventTypeToken {
			roTokens += ev.Content
		}
	}
	if !strings.Contains(roTokens, "EXECUTED:127.0.0.1") {
		t.Fatalf("expected human read_only ping to execute, got: %q", roTokens)
	}

	// 3. Human elevated command (nmap) MUST emit EventTypeSkillApprovalRequest and NOT execute
	outElev := make(chan llm.StreamEvent, 8)
	engine.RunFastPath(context.Background(), outElev, types.IntentMatch{
		Type: types.IntentTypeCommand,
		ID:   "nmap",
		Args: []string{"127.0.0.1"},
	}, convID)
	close(outElev)
	sawApproval := false
	for ev := range outElev {
		if ev.Type == llm.EventTypeSkillApprovalRequest && ev.SkillID == "vraxter-host-exec" {
			sawApproval = true
		}
		if strings.Contains(ev.Content, "EXECUTED:") {
			t.Fatalf("elevated nmap executed without HITL approval!")
		}
	}
	if !sawApproval {
		t.Fatal("expected EventTypeSkillApprovalRequest for elevated nmap command")
	}

	// 4. LLM-originated tool invocation of read_only ping MUST require HITL approval and yield
	outLLM := make(chan llm.StreamEvent, 8)
	llmPayload := `{"skill_id":"vraxter-host-exec","params":{"command":"ping","args":["127.0.0.1"]}}`
	feedback := engine.DispatchToolPayload(context.Background(), outLLM, llmPayload, "", convID)
	close(outLLM)
	if feedback != "!YIELD_FOR_APPROVAL" {
		t.Fatalf("expected LLM host-exec invocation to yield for HITL approval, got: %q", feedback)
	}
	sawLLMApproval := false
	for ev := range outLLM {
		if ev.Type == llm.EventTypeSkillApprovalRequest && ev.SkillID == "vraxter-host-exec" {
			sawLLMApproval = true
		}
		if strings.Contains(ev.Content, "EXECUTED:") {
			t.Fatalf("LLM-suggested host command executed without human confirmation!")
		}
	}
	if !sawLLMApproval {
		t.Fatal("expected EventTypeSkillApprovalRequest when LLM invokes vraxter-host-exec")
	}

	// 5. Orchestrator.ProcessRawIntent denies command injection "$" immediately without falling through to LLM
	cryptoSvc, _ := security.NewCryptoService(filepath.Join(dir, ".masterkey"))
	orch, err := core.NewOrchestrator(store, cryptoSvc, skills.NewRegistry(), engine.Runner, nil, nil, false, nil, false, "airgap", nil)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}
	orch.ExecEngine.HostExecGuard = engine.HostExecGuard

	stream, err := orch.ProcessRawIntent(context.Background(), convID, "$ping 127.0.0.1; whoami", "", "", "", "")
	if err != nil {
		t.Fatalf("ProcessRawIntent: %v", err)
	}
	var injOutput string
	for ev := range stream {
		injOutput += ev.Content
	}
	if !strings.Contains(injOutput, "Host command denied") || !strings.Contains(injOutput, "metacharacters") {
		t.Fatalf("expected immediate injection denial from orchestrator, got: %q", injOutput)
	}
}
