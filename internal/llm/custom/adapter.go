package custom

import (
	"strings"

	"github.com/patagonicrune/vraxter/internal/llm/openai"
	"github.com/patagonicrune/vraxter/internal/llm/registry"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
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
