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
	"testing"

	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/pkg/types"
)

func newTestRouter(t *testing.T, providers ...struct {
	p    *MockProvider
	caps string
}) *llm.Router {
	t.Helper()
	var entries []llm.RouterEntry
	for i, item := range providers {
		entries = append(entries, llm.RouterEntry{
			Provider: item.p,
			Config: types.ModelConfig{
				ID:           "mock-model-" + string(rune('a'+i)),
				ProviderID:   "p-mock-" + string(rune('a'+i)),
				Alias:        "mock-" + string(rune('a'+i)),
				Provider:     "mock",
				Model:        "mock-v1",
				Priority:     i + 1,
				IsActive:     true,
				Capabilities: item.caps,
			},
		})
	}
	return llm.NewRouterFromEntries(entries)
}

func TestRouter_GetOrderedProviders_ReturnsAll(t *testing.T) {
	p1 := &MockProvider{}
	p2 := &MockProvider{}
	router := newTestRouter(t,
		struct{ p *MockProvider; caps string }{p1, "text,tools"},
		struct{ p *MockProvider; caps string }{p2, "text"},
	)

	ordered := router.GetOrderedProviders()
	if len(ordered) != 2 {
		t.Errorf("expected 2 ordered providers, got %d", len(ordered))
	}
	if ordered[0].Config.Priority > ordered[1].Config.Priority {
		t.Error("expected providers ordered by priority ascending")
	}
}

func TestRouter_GetOrderedProviders_EmptyRouterReturnsNil(t *testing.T) {
	router := llm.NewRouterFromEntries(nil)
	ordered := router.GetOrderedProviders()
	if len(ordered) != 0 {
		t.Errorf("expected 0 ordered providers for empty router, got %d", len(ordered))
	}
}

func TestRouter_GetEmbeddingProvider_NoEmbeddingModel(t *testing.T) {
	p := &MockProvider{}
	router := newTestRouter(t, struct{ p *MockProvider; caps string }{p, "text,tools"})

	_, _, err := router.GetEmbeddingProvider()
	if err == nil {
		t.Fatal("expected error when no embedding-capable model is registered")
	}
}

func TestRouter_GetEmbeddingProvider_FindsCorrectModel(t *testing.T) {
	textOnly := &MockProvider{}
	embedProvider := &MockProvider{}
	router := newTestRouter(t,
		struct{ p *MockProvider; caps string }{textOnly, "text"},
		struct{ p *MockProvider; caps string }{embedProvider, "text,embedding"},
	)

	cfg, prov, err := router.GetEmbeddingProvider()
	if err != nil {
		t.Fatalf("GetEmbeddingProvider failed: %v", err)
	}
	if prov == nil {
		t.Fatal("expected non-nil embedding provider")
	}
	if cfg.Capabilities == "" {
		t.Error("expected non-empty capabilities for embedding model")
	}
}

func TestRouter_GetProvider_Success(t *testing.T) {
	p := &MockProvider{}
	router := newTestRouter(t, struct{ p *MockProvider; caps string }{p, "text"})

	cfg, prov, err := router.GetProvider(context.Background(), types.Intent{})
	if err != nil {
		t.Fatalf("GetProvider failed: %v", err)
	}
	if prov == nil {
		t.Fatal("expected non-nil provider")
	}
	if cfg.Alias == "" {
		t.Error("expected non-empty model alias")
	}
}

func TestRouter_GetProvider_EmptyReturnsError(t *testing.T) {
	router := llm.NewRouterFromEntries(nil)
	_, _, err := router.GetProvider(context.Background(), types.Intent{})
	if err == nil {
		t.Fatal("expected error for empty router, got nil")
	}
}

func TestRouter_Register_AddsDynamically(t *testing.T) {
	router := llm.NewRouter([]types.ModelConfig{
		{ID: "dynamic-1", ProviderID: "p-dyn", Alias: "dyn", Provider: "mock", Model: "mock", Priority: 1, IsActive: true},
	})
	p := &MockProvider{}
	router.Register("p-dyn", p)

	ordered := router.GetOrderedProviders()
	if len(ordered) != 1 {
		t.Errorf("expected 1 provider after Register, got %d", len(ordered))
	}
}
