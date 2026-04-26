package llm

import (
	"github.com/patagonicrune/vraxter/internal/llm/registry"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
)

// Type aliases to preserve backward compatibility for existing consumers
// while moving the canonical definitions to pkg/interfaces to avoid circularity.

type Message = interfaces.Message
type CompletionRequest = interfaces.CompletionRequest
type StreamEventType = interfaces.StreamEventType

const (
	EventTypeToken       = interfaces.EventTypeToken
	EventTypeError       = interfaces.EventTypeError
	EventTypeDone        = interfaces.EventTypeDone
	EventTypeSkillCall   = interfaces.EventTypeSkillCall
	EventTypeCommandCall = interfaces.EventTypeCommandCall
	EventTypePlanProposal = interfaces.EventTypePlanProposal
	EventTypeStatus       = interfaces.EventTypeStatus
	EventTypeSpecialistResult = interfaces.EventTypeSpecialistResult
)

type StreamEvent = interfaces.StreamEvent
type CompletionResponse = interfaces.CompletionResponse
type Provider = interfaces.LLMProvider

// Registry wrappers to avoid cross-package imports for core logic
func Create(pType, apiKey, baseURL string) (Provider, error) {
	return registry.Create(pType, apiKey, baseURL)
}

func GetSupportedProviders() []string {
	return registry.GetSupportedProviders()
}
