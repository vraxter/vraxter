// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

type stubLLMProvider struct {
	mu       sync.Mutex
	reply    string
	calls    int
	blockCh  chan struct{}
}

func (s *stubLLMProvider) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
	s.mu.Lock()
	s.calls++
	reply := s.reply
	s.mu.Unlock()
	return interfaces.CompletionResponse{Content: reply}, nil
}

func (s *stubLLMProvider) StreamGenerate(ctx context.Context, req interfaces.CompletionRequest) (<-chan interfaces.StreamEvent, error) {
	s.mu.Lock()
	s.calls++
	reply := s.reply
	blockCh := s.blockCh
	s.mu.Unlock()

	ch := make(chan interfaces.StreamEvent)
	go func() {
		defer close(ch)
		if blockCh != nil {
			select {
			case <-blockCh:
			case <-ctx.Done():
				return
			}
		}
		if !interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeToken, Content: reply}) {
			return
		}
		interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeDone})
	}()
	return ch, nil
}

func (s *stubLLMProvider) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("unused")
}
func (s *stubLLMProvider) Discover(ctx context.Context) ([]interfaces.ModelMetadata, error) {
	return nil, nil
}
func (s *stubLLMProvider) GetModelDetails(ctx context.Context, modelID string) (map[string]interface{}, error) {
	return nil, nil
}
func (s *stubLLMProvider) CheckHealth(ctx context.Context) error { return nil }

func newStubOrchestrator(t *testing.T, reply string, requireSkillApproval bool) (*Orchestrator, *db.Store, *stubLLMProvider) {
	t.Helper()
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	cryptoSvc, err := security.NewCryptoService(filepath.Join(t.TempDir(), ".masterkey"))
	if err != nil {
		t.Fatalf("NewCryptoService: %v", err)
	}

	reg := skills.NewRegistry()
	runner := skills.NewRunner()
	t.Cleanup(func() { runner.Close(context.Background()) })

	orch, err := NewOrchestrator(store, cryptoSvc, reg, runner, nil, false, nil, requireSkillApproval, "airgap", nil)
	if err != nil {
		t.Fatalf("NewOrchestrator: %v", err)
	}

	stub := &stubLLMProvider{reply: reply}
	router := llm.NewRouterFromEntries([]llm.RouterEntry{
		{
			Provider: stub,
			Config: types.ModelConfig{
				ID:            "local-stub",
				ProviderID:    "prov-local",
				Provider:      "ollama",
				Alias:         "stub-llm",
				Model:         "stub-model",
				IsActive:      true,
				Priority:      1,
				ContextWindow: 8192,
			},
		},
	})
	orch.StreamCoord = NewStreamCoordinator(router, false)
	orch.Resolver.llmRouter = router
	orch.Planner.router = router
	return orch, store, stub
}

func TestOrchestrator_HITLApproveRejectAndExecutePlan_NoDoubleRUnlockOrRace(t *testing.T) {
	orch, _, _ := newStubOrchestrator(t, "[VRAX_CHAT] Continuation acknowledged.", true)

	// Add specialist so we can also verify !EXECUTE_PLAN is NOT hijacked by @mention
	_ = orch.SpecialistRepo.CreateSpecialist(types.Specialist{
		ID:        "spec-sec",
		Name:      "security-auditor",
		Expertise: "Security",
		CreatedAt: time.Now(),
	})

	// 1. !APPROVE_SKILL must execute cleanly without IdentifyIntent interception, without double RUnlock panic,
	// and without mutating shared orch.ExecEngine.RequireSkillApproval.
	approvePayload, _ := json.Marshal(types.ToolCall{
		SkillID: "nonexistent-approved-skill",
		Params:  map[string]interface{}{"x": 1},
	})
	stream, err := orch.ProcessRawIntent(context.Background(), "conv-hitl-1", "!APPROVE_SKILL "+string(approvePayload), "", "", "", "")
	if err != nil {
		t.Fatalf("ProcessRawIntent(!APPROVE_SKILL): %v", err)
	}
	var approveOut string
	for ev := range stream {
		approveOut += ev.Content
	}
	if !strings.Contains(approveOut, "Skill Execution Approved") {
		t.Fatalf("expected '!APPROVE_SKILL' to be handled by control path (not dead code), got: %q", approveOut)
	}
	if !strings.Contains(approveOut, "Continuation acknowledged") {
		t.Fatalf("expected LLM continuation after !APPROVE_SKILL, got: %q", approveOut)
	}
	if !orch.ExecEngine.RequireSkillApproval {
		t.Fatal("shared RequireSkillApproval must never be mutated to false")
	}

	// 2. !REJECT_SKILL must execute cleanly without double RUnlock panic and continue to LLM
	rejectStream, err := orch.ProcessRawIntent(context.Background(), "conv-hitl-2", "!REJECT_SKILL "+string(approvePayload), "", "", "", "")
	if err != nil {
		t.Fatalf("ProcessRawIntent(!REJECT_SKILL): %v", err)
	}
	var rejectOut string
	for ev := range rejectStream {
		rejectOut += ev.Content
	}
	if !strings.Contains(rejectOut, "Skill Execution Rejected") {
		t.Fatalf("expected '!REJECT_SKILL' confirmation, got: %q", rejectOut)
	}
	if !strings.Contains(rejectOut, "Continuation acknowledged") {
		t.Fatalf("expected LLM continuation after !REJECT_SKILL, got: %q", rejectOut)
	}

	// 3. !EXECUTE_PLAN must NOT be intercepted by IdentifyIntent ("!") or @mention inside JSON,
	// and must synchronize phaseOutput cleanly via WaitGroup before reporting JOB_COMPLETED.
	eventsCh := orch.JobManager.Subscribe("test-plan-listener")
	defer orch.JobManager.Unsubscribe("test-plan-listener")

	plan := Plan{
		Goal: "Harden perimeter with @security-auditor",
		Phases: []Phase{
			{Title: "Phase Alpha", Description: "Inspect config", Specialist: "supervisor"},
			{Title: "Phase Beta", Description: "Verify rules", Specialist: "security-auditor"},
		},
	}
	planBytes, _ := json.Marshal(plan)
	planStream, err := orch.ProcessRawIntent(context.Background(), "conv-plan-1", "!EXECUTE_PLAN "+string(planBytes), "", "", "", "")
	if err != nil {
		t.Fatalf("ProcessRawIntent(!EXECUTE_PLAN): %v", err)
	}
	var planAck string
	for ev := range planStream {
		planAck += ev.Content
	}
	if !strings.Contains(planAck, "Spawned Plan Execution Daemon") {
		t.Fatalf("expected '!EXECUTE_PLAN' to spawn daemon, got: %q", planAck)
	}

	// Wait for EventJobCompleted and confirm both phases' output were safely accumulated
	timeout := time.After(5 * time.Second)
	var completedPayload string
	for completedPayload == "" {
		select {
		case sysEvt := <-eventsCh:
			if sysEvt.Type == EventJobCompleted && strings.Contains(sysEvt.Title, "Plan Execution") {
				completedPayload = sysEvt.Payload
			}
		case <-timeout:
			t.Fatal("timed out waiting for !EXECUTE_PLAN EventJobCompleted")
		}
	}
	if !strings.Contains(completedPayload, "Phase 1: Phase Alpha") || !strings.Contains(completedPayload, "Phase 2: Phase Beta") {
		t.Fatalf("expected both phase outputs in completed plan payload, got: %q", completedPayload)
	}
	if !strings.Contains(completedPayload, "Continuation acknowledged") {
		t.Fatalf("expected synchronized phase output text from sub-tasks, got: %q", completedPayload)
	}

	// 4. Concurrent !APPROVE_SKILL / !REJECT_SKILL / cache invalidation stress test (no mutex crash or race)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			cmd := "!APPROVE_SKILL " + string(approvePayload)
			if idx%2 == 1 {
				cmd = "!REJECT_SKILL " + string(approvePayload)
			}
			orch.InvalidateCache()
			s, err := orch.ProcessRawIntent(context.Background(), fmt.Sprintf("conv-conc-%d", idx), cmd, "", "", "", "")
			if err != nil {
				t.Errorf("concurrent ProcessRawIntent %d failed: %v", idx, err)
				return
			}
			for range s {
			}
		}(i)
	}
	wg.Wait()
	if !orch.ExecEngine.RequireSkillApproval {
		t.Fatal("RequireSkillApproval was corrupted under concurrent HITL requests")
	}
}

func TestOrchestrator_RefreshCacheNeverReinjectsCloudModelsInAirgapOrLocalLAN(t *testing.T) {
	for _, mode := range []string{"airgap", "local_lan"} {
		t.Run(mode, func(t *testing.T) {
			store, err := db.NewStore(":memory:")
			if err != nil {
				t.Fatalf("NewStore: %v", err)
			}
			defer store.Close()

			cryptoSvc, err := security.NewCryptoService(filepath.Join(t.TempDir(), ".masterkey"))
			if err != nil {
				t.Fatalf("NewCryptoService: %v", err)
			}

			provRepo := db.NewProviderRepository(store, cryptoSvc)
			modelRepo := db.NewModelRepository(store, cryptoSvc)

			// Seed 1 cloud provider, 1 WAN custom provider, and 1 local Ollama provider directly in DB
			_ = provRepo.Create(&db.Provider{ID: "p-openai", Name: "OpenAI", Type: "openai", APIKey: "sk-test", IsActive: true})
			_ = provRepo.Create(&db.Provider{ID: "p-wan", Name: "RemoteWAN", Type: "custom", BaseURL: "http://8.8.8.8:11434", IsActive: true})
			_ = provRepo.Create(&db.Provider{ID: "p-local", Name: "LocalOllama", Type: "ollama", BaseURL: "http://127.0.0.1:11434", IsActive: true})

			_ = modelRepo.UpsertModel(types.ModelConfig{ID: "m-cloud", ProviderID: "p-openai", Alias: "gpt4", Model: "gpt-4o", Priority: 1, IsActive: true})
			_ = modelRepo.UpsertModel(types.ModelConfig{ID: "m-wan", ProviderID: "p-wan", Alias: "wan-llm", Model: "llama3", Priority: 2, IsActive: true})
			_ = modelRepo.UpsertModel(types.ModelConfig{ID: "m-local", ProviderID: "p-local", Alias: "local-llama", Model: "llama3.2", Priority: 3, IsActive: true})

			reg := skills.NewRegistry()
			runner := skills.NewRunner()
			defer runner.Close(context.Background())

			orch, err := NewOrchestrator(store, cryptoSvc, reg, runner, nil, false, nil, false, mode, nil)
			if err != nil {
				t.Fatalf("NewOrchestrator(%s): %v", mode, err)
			}

			verifyOnlyLocal := func(stage string) {
				ordered := orch.StreamCoord.Router.GetOrderedProviders()
				if len(ordered) != 1 || ordered[0].Config.ID != "m-local" {
					t.Fatalf("[%s/%s] expected ONLY 'm-local' in router, got: %+v", mode, stage, ordered)
				}
				if active := orch.GetActiveModelID(""); active != "local-llama" {
					t.Fatalf("[%s/%s] expected active model 'local-llama', got %q", mode, stage, active)
				}
				if failover := orch.GetFailoverModelID(""); failover != "none" {
					t.Fatalf("[%s/%s] expected failover 'none' (no cloud fallback!), got %q", mode, stage, failover)
				}
			}

			verifyOnlyLocal("startup")

			// Force 30s metadata cache refresh (previously reinjected all DB cloud models!)
			orch.InvalidateCache()
			if err := orch.refreshCache(); err != nil {
				t.Fatalf("refreshCache failed: %v", err)
			}
			verifyOnlyLocal("after_refreshCache")

			orch.mu.RLock()
			cachedCtx := orch.cachedModelsCtx
			orch.mu.RUnlock()
			if strings.Contains(cachedCtx, "gpt-4o") || strings.Contains(cachedCtx, "m-cloud") || strings.Contains(cachedCtx, "m-wan") {
				t.Fatalf("[%s] refreshCache leaked disallowed cloud/WAN models into cachedModelsCtx:\n%s", mode, cachedCtx)
			}

			// Also verify empty-router lazy sync in GetActiveModelID / GetFailoverModelID filters cloud models
			orch.StreamCoord.Router.SyncActiveModels(nil)
			if active := orch.GetActiveModelID(""); active != "local-llama" {
				t.Fatalf("[%s/after_lazy_sync] expected active model 'local-llama', got %q", mode, active)
			}
			verifyOnlyLocal("after_lazy_sync")
		})
	}
}

func TestSwarmDispatcher_NoGoroutineLeak(t *testing.T) {
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	jm := NewJobManager()
	specRepo := db.NewSpecialistRepository(store)
	execEngine := NewExecutionEngine(skills.NewRegistry(), nil, nil, db.NewChatRepository(store), specRepo, jm, nil, false, false)
	execEngine.SwarmDelegate = func(ctx context.Context, out chan<- llm.StreamEvent, specialistID, task, threadID string) string {
		for i := 0; i < 5; i++ {
			interfaces.SendStreamEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: "tok"})
		}
		return "done:" + specialistID
	}

	swarm := NewSwarmDispatcher(execEngine, jm, nil)
	sub := jm.Subscribe("leak-check")
	defer jm.Unsubscribe("leak-check")

	runtime.GC()
	time.Sleep(30 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	const batches = 5
	for b := 0; b < batches; b++ {
		tasks := make([]SwarmTask, 10)
		for i := range tasks {
			tasks[i] = SwarmTask{SpecialistID: fmt.Sprintf("spec-%d", i), Query: "audit"}
		}
		jobID := swarm.Dispatch("sess-1", "LeakTest", tasks)

		deadline := time.After(5 * time.Second)
		finished := false
		for !finished {
			select {
			case ev := <-sub:
				if ev.JobID == jobID && (ev.Type == EventJobCompleted || ev.Type == EventJobFailed) {
					finished = true
				}
			case <-deadline:
				t.Fatalf("timed out waiting for swarm batch %d", b)
			}
		}
	}

	// Wait briefly for goroutines to unwind after close(outDrain) + drainWG.Wait()
	var after int
	for i := 0; i < 20; i++ {
		runtime.GC()
		time.Sleep(25 * time.Millisecond)
		after = runtime.NumGoroutine()
		if after <= baseline+3 {
			break
		}
	}
	if after > baseline+3 {
		t.Fatalf("goroutine leak detected in SwarmDispatcher: baseline=%d, after 50 swarm tasks=%d", baseline, after)
	}
}

func TestJobManager_BoundedRetention(t *testing.T) {
	const limit = 10
	jm := NewJobManagerWithLimit(limit)
	sub := jm.Subscribe("retention-check")
	defer jm.Unsubscribe("retention-check")

	const totalJobs = 45
	completed := 0
	for i := 0; i < totalJobs; i++ {
		jm.SpawnDaemon(fmt.Sprintf("job-%d", i), func() (string, error) {
			return "ok", nil
		})
	}

	deadline := time.After(5 * time.Second)
	for completed < totalJobs {
		select {
		case ev := <-sub:
			if ev.Type == EventJobCompleted || ev.Type == EventJobFailed {
				completed++
			}
		case <-deadline:
			t.Fatalf("timed out waiting for jobs to finish: %d/%d", completed, totalJobs)
		}
	}

	retained := jm.GetActiveJobs()
	if len(retained) > limit {
		t.Fatalf("expected JobManager to cap retained jobs at <= %d, got %d", limit, len(retained))
	}
	if len(retained) == 0 {
		t.Fatal("expected most recent jobs to remain retained up to cap")
	}

	// Running jobs must not be evicted while still active
	gate := make(chan struct{})
	runningID := jm.SpawnDaemon("long-running", func() (string, error) {
		<-gate
		return "done", nil
	})
	defer close(gate)

	foundRunning := false
	for _, j := range jm.GetActiveJobs() {
		if j.ID == runningID && j.Status == "running" {
			foundRunning = true
			break
		}
	}
	if !foundRunning {
		t.Fatalf("running job %s was unexpectedly missing from JobManager", runningID)
	}
}
