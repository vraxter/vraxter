package interfaces

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

// ModelMetadata represents a model discovered from a provider's registry
type ModelMetadata struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"display_name"`
	Description   string   `json:"description"`
	ContextWindow int      `json:"context_window"`
	Capabilities  []string `json:"capabilities"` // text, vision, tools, audio, image, embedding...
}

// LLMProvider represents a generic LLM API connection (Ollama, OpenAI, Anthropic)
type LLMProvider interface {
	Generate(ctx context.Context, req CompletionRequest) (CompletionResponse, error)
	StreamGenerate(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error)
	Embed(ctx context.Context, model string, texts []string) ([][]float32, error)
	Discover(ctx context.Context) ([]ModelMetadata, error)
	GetModelDetails(ctx context.Context, modelID string) (map[string]interface{}, error)
	CheckHealth(ctx context.Context) error
}
