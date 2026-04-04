package types

import "time"

// Specialist represents a sub-agent profile with restricted expertise
type Specialist struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Expertise    string    `json:"expertise"`
	ModelID      string    `json:"model_id,omitempty"`      // If empty, uses the engine's default model
	SystemPrompt string    `json:"system_prompt,omitempty"` // The cognitive enforcement prompt
	CreatedAt    time.Time `json:"created_at"`
}
