// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package types

import (
	"encoding/json"
	"strings"
)

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
	TimeoutSeconds int    `json:"timeout_seconds,omitempty"` // Configurable max execution time for WASM

	// Intent Resolver Metadata
	Keywords   []string `json:"keywords"`    // Exact word matching
	Examples   []string `json:"examples"`    // Semantic or UI examples
	Tags       []string `json:"tags"`        // Broad categorization
	ParamRegex string   `json:"param_regex"` // Regex pattern with named capture groups
	ParamsSchema string `json:"params_schema"` // JSON Schema or descriptive string of required parameters
	Vector     []float32 `json:"vector,omitempty"` // Runtime cached embedding vector
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

// ToolCall represents a strictly typed parsed tool invocation from the LLM.
type ToolCall struct {
	SkillID  string                 `json:"skill_id"`
	IsDaemon bool                   `json:"is_daemon,omitempty"`
	Params   map[string]interface{} `json:"params"`
}

// UnmarshalJSON implements custom logic to safely extract SkillID and unify params,
// regardless of whether the LLM nested them or flattened them.
func (t *ToolCall) UnmarshalJSON(data []byte) error {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	t.Params = make(map[string]interface{})

	for k, v := range raw {
		switch k {
		case "skill_id", "name", "tool_name", "action":
			if s, ok := v.(string); ok && t.SkillID == "" {
				t.SkillID = s
			}
		case "is_daemon":
			if b, ok := v.(bool); ok {
				t.IsDaemon = b
			}
		case "params", "parameters":
			if nestedMap, ok := v.(map[string]interface{}); ok {
				for nk, nv := range nestedMap {
					t.Params[nk] = nv
				}
			}
		default:
			// Flat parameter, put it in Params
			t.Params[k] = v
		}
	}

	// Normalize common LLM alias patterns to canonical skill IDs.
	t.SkillID = normalizeSkillID(t.SkillID)

	return nil
}

// skillAliases maps common LLM-hallucinated IDs to canonical Vraxter skill IDs.
var skillAliases = map[string]string{
	"read_file":          "vraxter-read-file",
	"read-file":          "vraxter-read-file",
	"readfile":           "vraxter-read-file",
	"vraxter-readfile":   "vraxter-read-file",
	"list_dir":           "vraxter-list-dir",
	"list-dir":           "vraxter-list-dir",
	"listdir":            "vraxter-list-dir",
	"vraxter-listdir":    "vraxter-list-dir",
	"list_files":         "vraxter-list-dir",
	"list-files":         "vraxter-list-dir",
	"listfiles":          "vraxter-list-dir",
	"vraxter-list-files": "vraxter-list-dir",
	"vraxter-listfiles":  "vraxter-list-dir",
	"coder":              "vraxter-coder",
	"patch_code":         "vraxter-patch-code",
	"patch-code":         "vraxter-patch-code",
}

// normalizeSkillID maps known aliases to their canonical form.
// Always checks the alias map first (even for "vraxter-" prefixed IDs)
// because the LLM may hallucinate valid-looking but wrong prefixed names.
func normalizeSkillID(id string) string {
	if canonical, ok := skillAliases[strings.ToLower(id)]; ok {
		return canonical
	}
	return id
}
