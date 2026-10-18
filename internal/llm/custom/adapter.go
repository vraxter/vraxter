// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package custom

import (
	"github.com/vraxter/vraxter/internal/llm/openai"
	"github.com/vraxter/vraxter/internal/llm/registry"
	"github.com/vraxter/vraxter/pkg/interfaces"
)

func init() {
	// 1. Generic custom provider for self-hosted OpenAI-compatible endpoints (vLLM, LM Studio, Ollama, etc.)
	registry.Register("custom", func(apiKey, baseURL string) interfaces.LLMProvider {
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return openai.NewAdapter(apiKey, baseURL)
	})

	// 2. Ollama alias: Routes to Ollama's native OpenAI-compatible endpoint without requiring an API key
	registry.Register("ollama", func(apiKey, baseURL string) interfaces.LLMProvider {
		if baseURL == "" {
			baseURL = "http://localhost:11434"
		}
		return openai.NewAdapter(apiKey, baseURL)
	})
}
