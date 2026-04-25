package llm

import "context"

// Message represents a single conversational turn or system instruction
type Message struct {
	Role    string `json:"role"` // "system", "user", "assistant"
	Content string `json:"content"`
}

// CompletionRequest is the standard struct to decouple Vraxter logic from specific APIs
type CompletionRequest struct {
	Model    string
	Messages []Message
	Format   string // Format "json" if structured output is needed
}

// StreamEventType defines what's coming through the channel (token, tool-call, error)
type StreamEventType string

const (
	EventTypeToken       StreamEventType = "token"
	EventTypeError       StreamEventType = "error"
	EventTypeDone        StreamEventType = "done"
	EventTypeSkillCall   StreamEventType = "skill_call"
	EventTypeCommandCall StreamEventType = "command_call"
	EventTypePlanProposal StreamEventType = "plan_proposal"
	EventTypeStatus       StreamEventType = "status"
	EventTypeSpecialistResult StreamEventType = "specialist_result"
)

// StreamEvent is the unit of communication for Vraxter's reactive architecture
type StreamEvent struct {
	Type          StreamEventType
	Content       string
	Err           error
	ActiveModelID string // The model ID currently driving the session
}

// CompletionResponse encapsulates what an LLM responds
type CompletionResponse struct {
	Content string
}

// Provider represents a generic LLM API connection (Ollama, OpenAI, Anthropic)

type Provider interface {
	Generate(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
	StreamGenerate(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error)
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
	CheckHealth(ctx context.Context) error
}
