// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

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
	// Deprecated: Priority is being replaced by UseCasePriorities for granular routing.
	Priority      int            `json:"priority"` 
	
	// UseCasePriorities maps a specific use-case tag (e.g. "coding") to a priority integer.
	// 0 is the highest priority. If a use-case is not found in this map, the router
	// will fall back to the default Priority integer.
	UseCasePriorities map[string]int `json:"use_case_priorities"`

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
