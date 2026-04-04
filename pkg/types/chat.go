package types

import "time"

// Intent represents an actionable user request sent to the Core
type Intent struct {
	ID        string    `json:"id"`
	Query     string    `json:"query"`
	Language  string    `json:"language"`
	Timestamp time.Time `json:"timestamp"`
}

// Conversation represents a chat session to group history
type Conversation struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	SpecialistID string    `json:"specialist_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// Message role definitions
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleSystem    = "system"
	RoleSkill     = "skill"
)

// Message represents a single conversational turn in the chat history
type Message struct {
	ID             string    `json:"id"`
	ConversationID string    `json:"conversation_id"`
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	TokensUsed     int       `json:"tokens_used"`
	Timestamp      time.Time `json:"timestamp"`
}
