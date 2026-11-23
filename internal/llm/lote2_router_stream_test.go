// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/llm/anthropic"
	"github.com/vraxter/vraxter/internal/llm/google"
	"github.com/vraxter/vraxter/internal/llm/openai"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

func TestClassifyUseCase_DeterministicOnTies(t *testing.T) {
	tieCases := []struct {
		query    string
		expected string // strictly sorted tie-break: analysis < coding < math < planning < writing
	}{
		{"code plan", "coding"},                           // coding=1, planning=1 -> coding
		{"write analyze", "analysis"},                     // analysis=1, writing=1 -> analysis
		{"calculate write code plan analyze", "analysis"}, // all 5 tied at 1 -> analysis
		{"calculate plan", "math"},                        // math=1, planning=1 -> math
		{"plan write", "planning"},                        // planning=1, writing=1 -> planning
	}

	for _, tc := range tieCases {
		for iter := 0; iter < 200; iter++ {
			got := ClassifyUseCase(tc.query)
			if got != tc.expected {
				t.Fatalf("non-deterministic tie break for %q on iter %d: expected %q, got %q", tc.query, iter, tc.expected, got)
			}
		}
	}
}

func TestRouter_ConcurrentRoutingOverridesSafety(t *testing.T) {
	r := NewRouter([]types.ModelConfig{
		{ID: "m1", ProviderID: "p1", Priority: 1},
		{ID: "m2", ProviderID: "p2", Priority: 2},
	})
	dummy := openai.NewAdapter("k", "http://127.0.0.1:11434")
	r.Register("p1", dummy)
	r.Register("p2", dummy)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if (idx+j)%2 == 0 {
					r.SetUseCaseOverride("coding", "m2")
				} else {
					r.SetUseCaseOverride("coding", "m1")
				}
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				ordered := r.GetOrderedProvidersForUseCase("coding")
				if len(ordered) != 2 {
					t.Errorf("expected 2 models, got %d", len(ordered))
				}
			}
		}()
	}
	wg.Wait()
}

func TestLLMAdapters_ContextCancelNoGoroutineLeak(t *testing.T) {
	// 1. OpenAI SSE server emitting continuous chunks
	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 50; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":\"chunk-%d\"}}]}\n\n", i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
	}))
	defer openaiSrv.Close()

	// 2. Google streaming JSON array server
	googleSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("["))
		for i := 0; i < 30; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			if i > 0 {
				_, _ = w.Write([]byte(","))
			}
			_, _ = fmt.Fprintf(w, `{"candidates":[{"content":{"parts":[{"text":"g-%d"}]}}]}`, i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(5 * time.Millisecond)
		}
		_, _ = w.Write([]byte("]"))
	}))
	defer googleSrv.Close()

	// 3. Anthropic JSON server (stream wrapper emits token+done to unbuffered channel)
	anthropicSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"text":"hello-anthropic"}]}`))
	}))
	defer anthropicSrv.Close()

	adapters := []struct {
		name string
		p    interfaces.LLMProvider
	}{
		{"openai", openai.NewAdapter("test-key", openaiSrv.URL)},
		{"google", google.NewAdapter("test-key", googleSrv.URL)},
		{"anthropic", anthropic.NewAdapter("test-key", anthropicSrv.URL)},
	}

	req := interfaces.CompletionRequest{
		Model:    "test-model",
		Messages: []interfaces.Message{{Role: "user", Content: "hi"}},
	}

	runtime.GC()
	time.Sleep(30 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	for _, tc := range adapters {
		for round := 0; round < 5; round++ {
			ctx, cancel := context.WithCancel(context.Background())
			ch, err := tc.p.StreamGenerate(ctx, req)
			if err != nil {
				cancel()
				t.Fatalf("[%s] StreamGenerate failed: %v", tc.name, err)
			}
			// Deliberately cancel WITHOUT draining the channel (simulating abrupt gRPC client disconnect)
			time.Sleep(15 * time.Millisecond)
			cancel()

			// Channel MUST close promptly without anyone reading remaining messages
			select {
			case _, ok := <-ch:
				if ok {
					// Drain at most 1 in-flight item then expect close
					select {
					case <-ch:
					case <-time.After(2 * time.Second):
						t.Fatalf("[%s] stream goroutine hung after ctx cancel", tc.name)
					}
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("[%s] stream channel never closed after ctx cancel", tc.name)
			}
		}
	}

	var after int
	for i := 0; i < 20; i++ {
		runtime.GC()
		time.Sleep(30 * time.Millisecond)
		after = runtime.NumGoroutine()
		if after <= baseline+6 {
			break
		}
	}
	if after > baseline+6 {
		t.Fatalf("goroutine leak after canceling LLM adapter streams: baseline=%d, after=%d", baseline, after)
	}
}
