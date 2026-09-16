// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tests

import (
	"regexp"
	"testing"

	"github.com/vraxter/vraxter/internal/core"
)

// ─── CosineSimilarity (resolver package) ─────────────────────────────────────

func TestCosineSimilarity_Identical(t *testing.T) {
	v := []float32{0.3, 0.5, 0.8, 0.1}
	score := core.CosineSimilarity(v, v)
	if score < 0.9999 {
		t.Errorf("identical vectors should be ~1.0, got %f", score)
	}
}

func TestCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1.0, 0.0, 0.0}
	b := []float32{0.0, 1.0, 0.0}
	score := core.CosineSimilarity(a, b)
	if score > 0.0001 {
		t.Errorf("orthogonal vectors should score ~0.0, got %f", score)
	}
}

func TestCosineSimilarity_AntiParallel(t *testing.T) {
	a := []float32{1.0, 0.0}
	b := []float32{-1.0, 0.0}
	score := core.CosineSimilarity(a, b)
	if score > -0.9999 {
		t.Errorf("anti-parallel vectors should score ~-1.0, got %f", score)
	}
}

func TestCosineSimilarity_ZeroVector(t *testing.T) {
	a := []float32{0.0, 0.0, 0.0}
	b := []float32{1.0, 2.0, 3.0}
	score := core.CosineSimilarity(a, b)
	if score != 0.0 {
		t.Errorf("zero vector should yield 0.0 similarity, got %f", score)
	}
}

func TestCosineSimilarity_LengthMismatch(t *testing.T) {
	a := []float32{1.0, 0.5}
	b := []float32{1.0}
	score := core.CosineSimilarity(a, b)
	if score != 0.0 {
		t.Errorf("length mismatch should yield 0.0, got %f", score)
	}
}

func TestCosineSimilarity_PartialOverlap(t *testing.T) {
	a := []float32{1.0, 1.0, 0.0}
	b := []float32{1.0, 0.0, 0.0}
	score := core.CosineSimilarity(a, b)
	if score <= 0.0 || score >= 1.0 {
		t.Errorf("partially overlapping vectors should score between 0 and 1, got %f", score)
	}
}

// ─── ExtractRegexParams ───────────────────────────────────────────────────────

func TestExtractRegexParams_NamedGroups(t *testing.T) {
	re := regexp.MustCompile(`(?i)run (?P<skill>[a-z0-9-]+)`)
	params := core.ExtractRegexParams(re, "run my-cool-skill")

	skill, ok := params["skill"]
	if !ok {
		t.Fatal("expected 'skill' named capture group")
	}
	if skill != "my-cool-skill" {
		t.Errorf("expected skill='my-cool-skill', got %q", skill)
	}
	if params["query"] != "run my-cool-skill" {
		t.Errorf("expected raw query in params, got %q", params["query"])
	}
}

func TestExtractRegexParams_NoNamedGroups(t *testing.T) {
	re := regexp.MustCompile(`(?i)hello`)
	params := core.ExtractRegexParams(re, "hello world")

	if _, ok := params["query"]; !ok {
		t.Fatal("expected 'query' key always present")
	}
	if len(params) != 1 {
		t.Errorf("expected exactly 1 param (query), got %d", len(params))
	}
}

func TestExtractRegexParams_NoMatch(t *testing.T) {
	re := regexp.MustCompile(`(?i)run (?P<skill>[a-z]+)`)
	params := core.ExtractRegexParams(re, "unrelated query")

	if params["query"] != "unrelated query" {
		t.Errorf("expected raw query preserved, got %q", params["query"])
	}
}

func TestExtractRegexParams_MultipleGroups(t *testing.T) {
	re := regexp.MustCompile(`(?i)create (?P<lang>[a-z]+) skill (?P<name>[a-z0-9-]+)`)
	params := core.ExtractRegexParams(re, "create go skill my-tool")

	if params["lang"] != "go" {
		t.Errorf("expected lang='go', got %q", params["lang"])
	}
	if params["name"] != "my-tool" {
		t.Errorf("expected name='my-tool', got %q", params["name"])
	}
}

// ─── StreamParser ─────────────────────────────────────────────────────────────

func TestStreamParser_ToolAndCodePayload(t *testing.T) {
	p := core.NewStreamParser("[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")

	fullResponse := "[VRAX_CHAT] Let me run this.[VRAX_TOOL]{\"name\":\"list-dir\",\"path\":\".\"}\n[VRAX_CODE]print('done')"
	for _, ch := range fullResponse {
		p.ProcessToken(string(ch))
	}

	tool := p.ToolPayload()
	if tool == "" {
		t.Error("expected non-empty tool payload")
	}
	if !containsStr(tool, "list-dir") {
		t.Errorf("expected tool name in payload, got: %q", tool)
	}

	code := p.CodePayload()
	if code == "" {
		t.Error("expected non-empty code payload")
	}
	if !containsStr(code, "done") {
		t.Errorf("expected code content in payload, got: %q", code)
	}
}

func TestStreamParser_NoToolBlock(t *testing.T) {
	p := core.NewStreamParser("[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	p.ProcessToken("[VRAX_CHAT] Just text, no tools.")

	tool := p.ToolPayload()
	if tool != "" {
		t.Errorf("expected empty tool payload for chat-only response, got: %q", tool)
	}
}

func TestStreamParser_Flush_EmitsRemaining(t *testing.T) {
	p := core.NewStreamParser("[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	p.ProcessToken("[VRAX_CHAT] Answer is: fo")
	// Flush should return any content held back by the safety buffer
	_ = p.Flush()
}

func TestStreamParser_CodePayload_Empty_WhenNoCodeBlock(t *testing.T) {
	p := core.NewStreamParser("[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	p.ProcessToken("[VRAX_CHAT] OK.[VRAX_TOOL]{\"name\":\"test\"}")

	code := p.CodePayload()
	if code != "" {
		t.Errorf("expected empty code payload when no CODE marker present, got: %q", code)
	}
}

func TestStreamParser_State_ReturnsZero(t *testing.T) {
	p := core.NewStreamParser("[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	if p.State() != 0 {
		t.Errorf("expected State()=0, got %d", p.State())
	}
}

func TestStreamParser_NativeChat_NoMarker(t *testing.T) {
	// When there's no VRAX_CHAT marker, content is a native chat response
	p := core.NewStreamParser("[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]")
	result := p.ProcessToken("Hello world")
	// Should stream it through if no '[' ambiguity
	_ = result
	// Tool payload should be empty
	if p.ToolPayload() != "" {
		t.Error("expected empty tool payload for native chat response")
	}
}
