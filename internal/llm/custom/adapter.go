// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package custom

import (
	"strings"

	"github.com/vraxter/vraxter/internal/llm/openai"
	"github.com/vraxter/vraxter/internal/llm/registry"
	"github.com/vraxter/vraxter/pkg/interfaces"
)

func init() {
	// 1. Generic custom provider for self-hosted OpenAI-compatible endpoints (vLLM, LM Studio, etc.)
	registry.Register("custom", func(apiKey, baseURL string) interfaces.LLMProvider {
		return openai.NewAdapter(apiKey, baseURL)
	})

	// 2. Ollama legacy alias: Routes to Ollama's native OpenAI-compatible endpoint
	registry.Register("ollama", func(apiKey, baseURL string) interfaces.LLMProvider {
		if baseURL == "" {
			baseURL = "http://localhost:11434/v1"
		}
		if !strings.HasSuffix(baseURL, "/v1") && !strings.Contains(baseURL, "/v1/") {
			baseURL = strings.TrimSuffix(baseURL, "/") + "/v1"
		}
		return openai.NewAdapter("ollama", baseURL)
	})
}
