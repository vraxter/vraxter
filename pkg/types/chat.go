package types

import "time"

const (
	SkillExecutionPrefix   = "!"
	CommandExecutionPrefix = "$"
	IntentTypeSkill           = "skill-execution"
	IntentTypeCommand         = "command-execution"
	IntentTypeSpecialist      = "specialist-delegation"
	IntentTypeReturnControl   = "return-control"
)

// MatchSignal classifies WHY a specialist match occurred.
// Each signal maps to a deterministic confidence score via its Confidence() method.
type MatchSignal int

const (
	// SignalNone indicates no specialist match was found.
	SignalNone MatchSignal = iota
	// SignalExplicitMention: User used @SpecialistName — unambiguous structural delegation.
	SignalExplicitMention
	// SignalDomainMatch: Query matches specialist expertise WITHOUT naming the specialist.
	SignalDomainMatch
	// SignalNameMention: Query contains specialist name WITHOUT @ prefix — ambiguous intent.
	SignalNameMention
	// SignalReturnControl: User used @Vraxter — explicit handoff back to supervisor.
	SignalReturnControl
)

// Confidence returns the deterministic score for this signal type.
// This is the single source of truth — no magic floats elsewhere in the resolver.
func (s MatchSignal) Confidence() float64 {
	switch s {
	case SignalExplicitMention:
		return 1.0
	case SignalReturnControl:
		return 1.0
	case SignalDomainMatch:
		return 0.9
	case SignalNameMention:
		return 0.6
	default:
		return 0.0
	}
}

// Intent represents an actionable user request sent to the Core
type Intent struct {
	ID         string    `json:"id"`
	Query      string    `json:"query"`
	Language   string    `json:"language"`
	SourceZone string    `json:"source_zone,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// IntentMatch represents the outcome of the IntentResolver
type IntentMatch struct {
	Type       string                 `json:"type"`
	ID         string                 `json:"id"`
	Confidence float64                `json:"confidence"`
	Signal     MatchSignal            `json:"signal"`
	Params     map[string]interface{} `json:"params"`
	Args       []string               `json:"args"`
}

// Conversation represents a chat session to group history
type Conversation struct {
	ID           string    `json:"id"`
	Title        string    `json:"title"`
	Summary      string    `json:"summary,omitempty"`
	SpecialistID string    `json:"specialist_id,omitempty"`
	MessageCount int       `json:"message_count,omitempty"`
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
