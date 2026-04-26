package tests

import (
	"context"
	"testing"

	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/pkg/types"
)

// mockRouter builds a minimal llm.Router from a slice of providers for testing.
func mockRouter(t *testing.T, providers ...*MockProvider) *llm.Router {
	t.Helper()
	var entries []llm.RouterEntry
	for i, p := range providers {
		entries = append(entries, llm.RouterEntry{
			Provider: p,
			Config: types.ModelConfig{
				ID:         "mock-model",
				ProviderID: "p-mock",
				Alias:      "mock",
				Provider:   "mock",
				Model:      "mock-v1",
				Priority:   i + 1,
				IsActive:   true,
			},
		})
	}
	return llm.NewRouterFromEntries(entries)
}

func TestStreamCoordinator_SuccessfulStream(t *testing.T) {
	provider := &MockProvider{
		Response: "[VRAX_CHAT] Hello, I am Vraxter.",
	}
	router := mockRouter(t, provider)
	sc := core.NewStreamCoordinator(router, false)

	out := make(chan llm.StreamEvent, 64)
	queue := make(chan types.Event, 64)

	req := llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "hello"}},
	}

	err := sc.Run(context.Background(), out, queue, req, "[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	if err != nil {
		t.Fatalf("StreamCoordinator.Run failed: %v", err)
	}

	close(out)
	close(queue)

	// Drain and verify we got meaningful events
	var tokens []string
	for ev := range out {
		if ev.Type == llm.EventTypeToken {
			tokens = append(tokens, ev.Content)
		}
	}
	if len(tokens) == 0 {
		t.Error("expected at least one token event from successful stream")
	}
}

func TestStreamCoordinator_FallbackOnFirstProviderError(t *testing.T) {
	failProvider := &MockProvider{ErrOnCall: errTest("primary provider down")}
	successProvider := &MockProvider{Response: "[VRAX_CHAT] Fallback response OK"}

	router := mockRouter(t, failProvider, successProvider)
	sc := core.NewStreamCoordinator(router, false)

	out := make(chan llm.StreamEvent, 64)
	queue := make(chan types.Event, 64)

	req := llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "test fallback"}},
	}

	err := sc.Run(context.Background(), out, queue, req, "[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	if err != nil {
		t.Fatalf("expected fallback to succeed, got err: %v", err)
	}
	close(out)
	close(queue)
}

func TestStreamCoordinator_AllProvidersFailReturnsError(t *testing.T) {
	fail1 := &MockProvider{ErrOnCall: errTest("provider 1 down")}
	fail2 := &MockProvider{ErrOnCall: errTest("provider 2 down")}

	router := mockRouter(t, fail1, fail2)
	sc := core.NewStreamCoordinator(router, false)

	out := make(chan llm.StreamEvent, 64)
	queue := make(chan types.Event, 64)

	req := llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "fail"}},
	}

	err := sc.Run(context.Background(), out, queue, req, "[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	if err == nil {
		t.Fatal("expected error when all providers fail, got nil")
	}
}

func TestStreamCoordinator_NoProvidersReturnsError(t *testing.T) {
	router := llm.NewRouterFromEntries(nil)
	sc := core.NewStreamCoordinator(router, false)

	out := make(chan llm.StreamEvent, 4)
	queue := make(chan types.Event, 4)

	req := llm.CompletionRequest{}
	err := sc.Run(context.Background(), out, queue, req, "[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	if err == nil {
		t.Fatal("expected error for empty provider list, got nil")
	}
}

func TestStreamCoordinator_ToolBlockTriggersQueueEvent(t *testing.T) {
	provider := &MockProvider{
		Response:  "",
		ToolBlock: "[VRAX_TOOL]{\"name\":\"vraxter-list-dir\",\"path\":\".\"}}",
	}
	router := mockRouter(t, provider)
	sc := core.NewStreamCoordinator(router, false)

	out := make(chan llm.StreamEvent, 64)
	queue := make(chan types.Event, 64)

	req := llm.CompletionRequest{
		Messages: []llm.Message{{Role: "user", Content: "list files"}},
	}

	err := sc.Run(context.Background(), out, queue, req, "[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	close(out)
	close(queue)

	var hasToolCall bool
	for ev := range queue {
		if ev.Type == types.EventTypeToolCall {
			hasToolCall = true
		}
	}
	if !hasToolCall {
		t.Error("expected EventTypeToolCall to be emitted to the queue when tool block is present")
	}
}

// errTest is a simple sentinel error for table-driven testing.
type testErr string

func (e testErr) Error() string { return string(e) }
func errTest(msg string) error  { return testErr(msg) }
