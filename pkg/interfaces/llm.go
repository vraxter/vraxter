package interfaces

import (
	"context"
	"strings"
)

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
	EventTypeSkillApprovalRequest StreamEventType = "skill_approval_request"
)

// StreamEvent is the unit of communication for Vraxter's reactive architecture
type StreamEvent struct {
	Type          StreamEventType
	Content       string
	Err           error
	ActiveModelID string // The model ID currently driving the session
	SkillID       string // Used for tool calls or approvals
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

// SpeechSynthesizer is an optional interface for models/providers that can synthesize text into speech
type SpeechSynthesizer interface {
	SynthesizeSpeech(ctx context.Context, text, voice string) ([]byte, error)
}

// AudioTranscriber is an optional interface for models/providers that can transcribe audio into text
type AudioTranscriber interface {
	TranscribeAudio(ctx context.Context, audioBytes []byte, mimeType, prompt string) (string, error)
}

// ParseDataURI checks if the given content starts with a base64 data URI format,
// e.g., "data:image/png;base64,iVBORw..." and parses it into mimeType, base64 data,
// and any trailing text prompt.
func ParseDataURI(content string) (mimeType string, base64Data string, textPrompt string, ok bool) {
	if !strings.HasPrefix(content, "data:") {
		return "", "", "", false
	}
	idx := strings.Index(content, ";base64,")
	if idx == -1 {
		return "", "", "", false
	}
	mimeType = content[5:idx]
	remaining := content[idx+8:]

	// Separate the base64 data from any trailing prompt text
	spaceIdx := strings.IndexAny(remaining, " \t\n\r")
	if spaceIdx == -1 {
		return mimeType, remaining, "", true
	}
	base64Data = remaining[:spaceIdx]
	textPrompt = strings.TrimSpace(remaining[spaceIdx:])
	return mimeType, base64Data, textPrompt, true
}
