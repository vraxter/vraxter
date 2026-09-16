// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tests

import (
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/prompts"
)

func TestRenderBaseSystem_ContainsExpectedBlocks(t *testing.T) {
	params := prompts.BaseSystemParams{
		ExistingSpecialists: "Go Expert, Security Auditor",
		AvailableModels:     "gemini-2.0-flash (Priority 1), ollama/llama3 (Priority 2)",
		AvailableTools:      "vraxter-read-file, vraxter-list-dir",
		MarkerChat:          "[VRAX_CHAT]",
		MarkerTool:          "[VRAX_TOOL]",
		MarkerCode:          "[VRAX_CODE]",
		GitContext:          "WORKSPACE GIT CONTEXT:\n M internal/core/orchestrator.go",
	}

	result, err := prompts.RenderBaseSystem(params)
	if err != nil {
		t.Fatalf("RenderBaseSystem failed: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty rendered prompt")
	}

	// Verify injected values appear in the output
	for _, expected := range []string{
		"Go Expert",
		"gemini-2.0-flash",
		"vraxter-read-file",
		"[VRAX_CHAT]",
		"[VRAX_TOOL]",
		"WORKSPACE GIT CONTEXT",
	} {
		if !strings.Contains(result, expected) {
			t.Errorf("expected %q in base system prompt, but not found", expected)
		}
	}
}

func TestRenderBaseSystem_EmptyGitContext(t *testing.T) {
	params := prompts.BaseSystemParams{
		ExistingSpecialists: "",
		AvailableModels:     "gemini-2.0-flash",
		AvailableTools:      "vraxter-list-dir",
		MarkerChat:          "[VRAX_CHAT]",
		MarkerTool:          "[VRAX_TOOL]",
		MarkerCode:          "[VRAX_CODE]",
		GitContext:          "",
	}

	result, err := prompts.RenderBaseSystem(params)
	if err != nil {
		t.Fatalf("RenderBaseSystem failed with empty git context: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result even with empty git context")
	}
}

func TestRenderSpecialist_ContainsExpectedBlocks(t *testing.T) {
	params := prompts.SpecialistParams{
		SpecialistName:      "Go Expert",
		SpecialistExpertise: "Go programming, concurrency, microservices",
		SpecialistPrompt:    "You are an expert Go developer. Focus on idiomatic patterns.",
		AvailableTools:      "vraxter-patch-code",
		EnforcementSuffix:   "Always respond in Go.",
		GitContext:          "WORKSPACE GIT CONTEXT:\n M main.go",
	}

	result, err := prompts.RenderSpecialist(params)
	if err != nil {
		t.Fatalf("RenderSpecialist failed: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty specialist prompt")
	}

	for _, expected := range []string{
		"Go Expert",
		"Go programming",
		"vraxter-patch-code",
		"WORKSPACE GIT CONTEXT",
	} {
		if !strings.Contains(result, expected) {
			t.Errorf("expected %q in specialist prompt, but not found", expected)
		}
	}
}

func TestRenderSpecialist_EmptyOptionalFields(t *testing.T) {
	params := prompts.SpecialistParams{
		SpecialistName:      "Minimal",
		SpecialistExpertise: "security",
		SpecialistPrompt:    "",
		AvailableTools:      "",
		EnforcementSuffix:   "",
		GitContext:          "",
	}

	result, err := prompts.RenderSpecialist(params)
	if err != nil {
		t.Fatalf("RenderSpecialist with minimal params failed: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result for minimal params")
	}
}
