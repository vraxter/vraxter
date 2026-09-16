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
	"strings"

	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/pkg/interfaces"
)

// MockProvider is a deterministic LLM stub for offline testing.
// It emits a pre-recorded sequence of tokens and an optional tool block.
type MockProvider struct {
	Response  string // Chat text that will be streamed token by token
	ToolBlock string // Optional VRAX_TOOL block to inject at the end
	ErrOnCall error  // If set, StreamGenerate returns this error immediately
	HealthErr error  // If set, CheckHealth returns this error
}

func (m *MockProvider) Generate(_ context.Context, _ llm.CompletionRequest) (llm.CompletionResponse, error) {
	if m.ErrOnCall != nil {
		return llm.CompletionResponse{}, m.ErrOnCall
	}
	return llm.CompletionResponse{Content: m.Response}, nil
}

func (m *MockProvider) StreamGenerate(_ context.Context, _ llm.CompletionRequest) (<-chan llm.StreamEvent, error) {
	if m.ErrOnCall != nil {
		return nil, m.ErrOnCall
	}

	ch := make(chan llm.StreamEvent, 32)
	go func() {
		defer close(ch)
		// Emit response word by word so parser tests are realistic
		words := strings.Fields(m.Response)
		for _, w := range words {
			ch <- llm.StreamEvent{Type: llm.EventTypeToken, Content: w + " "}
		}
		if m.ToolBlock != "" {
			ch <- llm.StreamEvent{Type: llm.EventTypeToken, Content: m.ToolBlock}
		}
		ch <- llm.StreamEvent{Type: llm.EventTypeDone}
	}()

	return ch, nil
}

func (m *MockProvider) Embed(_ context.Context, _ string, texts []string) ([][]float32, error) {
	if m.ErrOnCall != nil {
		return nil, m.ErrOnCall
	}
	// Return deterministic unit vectors based on text length
	result := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, 4)
		v[0] = float32(len(t)) / 100.0
		v[1] = 0.5
		v[2] = 0.3
		v[3] = 0.1
		result[i] = v
	}
	return result, nil
}

func (m *MockProvider) CheckHealth(_ context.Context) error {
	return m.HealthErr
}

func (m *MockProvider) Discover(_ context.Context) ([]interfaces.ModelMetadata, error) {
	return []interfaces.ModelMetadata{
		{ID: "mock-model-v1", DisplayName: "Mock Model V1", Capabilities: []string{"text"}},
		{ID: "mock-model-v2", DisplayName: "Mock Model V2", Capabilities: []string{"text", "vision"}},
	}, nil
}

func (m *MockProvider) GetModelDetails(_ context.Context, modelID string) (map[string]interface{}, error) {
	return map[string]interface{}{"id": modelID, "mock": true}, nil
}
