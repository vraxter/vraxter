package types

// ModelConfig represents an LLM configuration stored in the local SQlite DB
type ModelConfig struct {
	ID            string `json:"id"`
	Alias         string `json:"alias"`    // User-friendly name, e.g. "Ollama Local"
	Provider      string `json:"provider"` // "ollama", "openai", "anthropic", "gemini"
	Model         string `json:"model"`    // Model string, e.g. "llama3" or "gpt-4o"
	APIKey        string `json:"api_key"`
	BaseURL       string `json:"base_url"`  // Custom URL needed for local inferencing
	Priority      int    `json:"priority"`  // Ordering priority, 0 is preferred
	IsActive      bool   `json:"is_active"` // Globally turn on/off without deleting
	Capabilities  string `json:"capabilities"` // Comma separated capabilities: "vision,tools,1M-context"
	ContextWindow int    `json:"context_window"` // Stored explicit size
}
