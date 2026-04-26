package types

// ModelConfig represents an LLM configuration stored in the local SQlite DB
type ModelConfig struct {
	ID            string `json:"id"`
	ProviderID    string `json:"provider_id"`
	Alias         string `json:"alias"`    // User-friendly name, e.g. "Ollama Local"
	Provider      string `json:"provider"` // Provider TYPE: "ollama", "openai", "anthropic", "google"
	Model         string `json:"model"`    // Model string, e.g. "llama3" or "gpt-4o"
	APIKey        string `json:"api_key"`
	BaseURL       string `json:"base_url"`  // Custom URL needed for local inferencing
	Priority      int    `json:"priority"`  // Ordering priority, 0 is preferred
	IsActive      bool   `json:"is_active"` // Globally turn on/off without deleting
	Capabilities  string `json:"capabilities"`  // Comma separated capabilities: "vision,tools,1M-context"
	ContextWindow int    `json:"context_window"` // Stored explicit size
	UseCases      string `json:"use_cases"`      // Comma separated use-case tags: "coding,writing,analysis"
	IsConfigured  bool   `json:"is_configured"`  // Derived: does the parent provider have valid credentials?
}

// LLMResponse is the structured format expected uniformly from ALL providers
type LLMResponse struct {
	Action  string                 `json:"action"`              // "chat" or "skill"
	SkillID string                 `json:"skill_id,omitempty"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Content string                 `json:"content,omitempty"`
}
