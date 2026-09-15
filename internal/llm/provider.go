// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

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
	EventTypeSkillApprovalRequest = interfaces.EventTypeSkillApprovalRequest
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
