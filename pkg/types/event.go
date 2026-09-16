// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package types

// EventType represents the category of the autonomous system event
type EventType string

const (
	EventTypeText     EventType = "text"
	EventTypeToolCall EventType = "tool_call"
	EventTypeDone     EventType = "done"
	EventTypeError    EventType = "error"
)

// Event describes an asynchronous message passed between parser and executor
type Event struct {
	Type    EventType
	Payload any // Usually string for text/error, or map[string]interface{} / custom structs for ToolCalls
}
