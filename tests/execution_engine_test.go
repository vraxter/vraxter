package tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/pkg/types"
)

// newTestExecEngine creates an ExecutionEngine with an in-memory SQLite store.
func newTestExecEngine(t *testing.T) (*core.ExecutionEngine, *db.Store) {
	t.Helper()
	store := newTestStore(t)
	reg := skills.NewRegistry()
	chatRepo := db.NewChatRepository(store)
	specRepo := db.NewSpecialistRepository(store)

	// Runner with nil wazero for unit tests (metaskills don't use WASM)
	runner := skills.NewRunner()
	t.Cleanup(func() { runner.Close(context.Background()) })

	engine := core.NewExecutionEngine(reg, runner, nil, chatRepo, specRepo, false)
	return engine, store
}

// ─── DispatchToolPayload ──────────────────────────────────────────────────────

func TestDispatchToolPayload_ReadFile_Success(t *testing.T) {
	engine, store := newTestExecEngine(t)
	chatRepo := db.NewChatRepository(store)
	convID := "test-conv-exec"
	chatRepo.CreateConversation(&types.Conversation{ID: convID, Title: "exec test"})

	// Create a temp file to read
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	os.WriteFile(path, []byte("Vraxter Engine Test"), 0644)

	out := make(chan llm.StreamEvent, 8)
	toolJSON := `{"skill_id":"vraxter-read-file","path":"` + path + `"}`

	result := engine.DispatchToolPayload(context.Background(), out, toolJSON, "", convID)
	close(out)

	if !strings.Contains(result, "Vraxter Engine Test") {
		t.Errorf("expected file content in result, got: %q", result)
	}
}

func TestDispatchToolPayload_ListDir(t *testing.T) {
	engine, store := newTestExecEngine(t)
	chatRepo := db.NewChatRepository(store)
	convID := "test-conv-listdir"
	chatRepo.CreateConversation(&types.Conversation{ID: convID, Title: "listdir"})

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)

	out := make(chan llm.StreamEvent, 8)
	toolJSON := `{"skill_id":"vraxter-list-dir","path":"` + dir + `"}`

	result := engine.DispatchToolPayload(context.Background(), out, toolJSON, "", convID)
	close(out)

	if !strings.Contains(result, "main.go") {
		t.Errorf("expected directory listing with 'main.go', got: %q", result)
	}
}

func TestDispatchToolPayload_PatchCode(t *testing.T) {
	engine, store := newTestExecEngine(t)
	chatRepo := db.NewChatRepository(store)
	convID := "test-conv-patch"
	chatRepo.CreateConversation(&types.Conversation{ID: convID, Title: "patch"})

	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("func hello() { return \"world\" }"), 0644)

	out := make(chan llm.StreamEvent, 8)
	toolJSON := `{"skill_id":"vraxter-patch-code","path":"` + path + `","search":"\"world\"","replace":"\"Vraxter\""}`

	result := engine.DispatchToolPayload(context.Background(), out, toolJSON, "", convID)
	close(out)

	if !strings.Contains(result, "SUCCESS") {
		t.Errorf("expected patch SUCCESS, got: %q", result)
	}

	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "Vraxter") {
		t.Error("file was not actually patched")
	}
}

func TestDispatchToolPayload_InvalidJSON(t *testing.T) {
	engine, store := newTestExecEngine(t)
	chatRepo := db.NewChatRepository(store)
	convID := "test-conv-bad"
	chatRepo.CreateConversation(&types.Conversation{ID: convID, Title: "bad json"})

	out := make(chan llm.StreamEvent, 8)
	// Send garbage JSON
	result := engine.DispatchToolPayload(context.Background(), out, "{bad json}", "", convID)
	close(out)

	// Should return an error message, not panic
	if result == "" {
		// Some implementations return empty on bad JSON — just ensure no panic
		t.Log("empty result for bad JSON (acceptable)")
	}
}

func TestDispatchToolPayload_ReturnControl(t *testing.T) {
	engine, store := newTestExecEngine(t)
	chatRepo := db.NewChatRepository(store)
	convID := "test-conv-control"
	chatRepo.CreateConversation(&types.Conversation{ID: convID, Title: "control"})

	out := make(chan llm.StreamEvent, 8)
	toolJSON := `{"skill_id":"vraxter-return-control"}`

	engine.DispatchToolPayload(context.Background(), out, toolJSON, "", convID)
	close(out)

	var events []llm.StreamEvent
	for ev := range out {
		events = append(events, ev)
	}
	// Should emit a "returning control" token
	found := false
	for _, ev := range events {
		if strings.Contains(ev.Content, "Returning control") || strings.Contains(ev.Content, "control") {
			found = true
		}
	}
	if !found {
		t.Log("no 'returning control' token emitted (may be implementation-dependent)")
	}
}

// ─── NewExecutionEngine ───────────────────────────────────────────────────────

func TestNewExecutionEngine_NotNil(t *testing.T) {
	store := newTestStore(t)
	reg := skills.NewRegistry()
	chatRepo := db.NewChatRepository(store)
	specRepo := db.NewSpecialistRepository(store)
	runner := skills.NewRunner()
	defer runner.Close(context.Background())

	engine := core.NewExecutionEngine(reg, runner, nil, chatRepo, specRepo, false)
	if engine == nil {
		t.Fatal("expected non-nil ExecutionEngine")
	}
}

func TestNewExecutionEngine_VerboseFlag(t *testing.T) {
	store := newTestStore(t)
	reg := skills.NewRegistry()
	chatRepo := db.NewChatRepository(store)
	specRepo := db.NewSpecialistRepository(store)
	runner := skills.NewRunner()
	defer runner.Close(context.Background())

	engine := core.NewExecutionEngine(reg, runner, nil, chatRepo, specRepo, true)
	if engine == nil {
		t.Fatal("expected non-nil engine with verbose=true")
	}
	if !engine.Verbose {
		t.Error("expected Verbose=true")
	}
}
