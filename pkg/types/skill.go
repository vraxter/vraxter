package types

// SkillTier defines the level of trust for a skill
type SkillTier int

const (
	Tier0Core SkillTier = iota
	Tier1Official
	Tier2CommunityVerified
	Tier3Unverified
)

// SkillManifest represents the metadata of a skill
type SkillManifest struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Version     string    `json:"version"`
	Command     string    `json:"command"`  // The binary name, .wasm path, or command to execute
	Language    string    `json:"language"` // e.g., "go", "rust"
	Engine      string    `json:"engine"`   // "wasm" or "native"
	Tier        SkillTier `json:"tier"`
	Checksum    string    `json:"checksum"` // SHA-256 for integrity
	Permissions []string  `json:"permissions"`
	Score       float64   `json:"score"`
	Downloads   int       `json:"downloads"`
	IsOfficial  bool      `json:"is_official"`
}

// ExecutionStatus represents the current state of a skill's execution
type ExecutionStatus string

const (
	StatusRunning   ExecutionStatus = "running"
	StatusCompleted ExecutionStatus = "completed"
	StatusFailed    ExecutionStatus = "failed"
	StatusPaused    ExecutionStatus = "paused" // For when the skill needs user confirmation
)

// ExecutionResult represents the output from a skill
type ExecutionResult struct {
	IntentID string          `json:"intent_id"`
	Status   ExecutionStatus `json:"status"`
	Output   string          `json:"output"` // JSON or plain text answer
	Error    string          `json:"error,omitempty"`
}

// SkillRequest represents the JSON-RPC struct sent via Stdin to the skill
type SkillRequest struct {
	JSONRPC string                 `json:"jsonrpc"` // Usually "2.0"
	Method  string                 `json:"method"`  // E.g., "execute"
	Params  map[string]interface{} `json:"params"`
	ID      string                 `json:"id"`
}

// SkillResponse is what the skill returns via Stdout
type SkillResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	Result  *ExecutionResult `json:"result,omitempty"`
	Error   *RPCError        `json:"error,omitempty"`
	ID      string           `json:"id"`
}

// RPCError captures JSON-RPC format errors
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
