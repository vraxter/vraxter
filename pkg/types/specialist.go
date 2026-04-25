package types

import "time"

type Specialist struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Expertise    string    `json:"expertise"`
	ModelID      string    `json:"model_id,omitempty"`
	SystemPrompt string    `json:"system_prompt,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}
