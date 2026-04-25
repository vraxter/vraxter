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
