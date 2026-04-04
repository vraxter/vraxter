package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/pkg/types"
	"github.com/patagonicrune/vraxter/pkg/vraxerror"
)

// ExecutionEngine is responsible for running skills (both LLM-directed and fast-path),
// logging execution events, and emitting result events to the output channel.
type ExecutionEngine struct {
	Registry  *skills.Registry
	Runner    *skills.Runner
	Coder     *services.CoderService
	ChatRepo  *db.ChatRepository
	SpecRepo  *db.SpecialistRepository
	Verbose   bool
}

// NewExecutionEngine creates a new ExecutionEngine.
func NewExecutionEngine(
	registry *skills.Registry,
	runner *skills.Runner,
	coder *services.CoderService,
	chatRepo *db.ChatRepository,
	specRepo *db.SpecialistRepository,
	verbose bool,
) *ExecutionEngine {
	return &ExecutionEngine{
		Registry: registry,
		Runner:   runner,
		Coder:    coder,
		ChatRepo: chatRepo,
		SpecRepo: specRepo,
		Verbose:  verbose,
	}
}

// RunFastPath executes a pre-resolved skill match without consulting the LLM.
// It emits a SkillCall event, runs the skill, and emits a Token event with the result.
func (e *ExecutionEngine) RunFastPath(ctx context.Context, out chan<- llm.StreamEvent, match types.IntentMatch, conversationID string) {
	out <- llm.StreamEvent{Type: llm.EventTypeSkillCall, Content: match.SkillID}

	manifest, err := e.Registry.FindSkill(match.SkillID)
	if err != nil {
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n❌ Skill not found: %s", match.SkillID)}
		return
	}

	e.ChatRepo.SaveMessage(&types.Message{
		ID:             uuid.New().String(),
		ConversationID: conversationID,
		Role:           "skill",
		Content:        fmt.Sprintf("Invoked %s (Fast Path)", match.SkillID),
		Timestamp:      time.Now(),
	})

	result, err := e.Runner.Execute(ctx, *manifest, match.Params)
	if err != nil {
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n❌ Skill execution failed: %v", err)}
		return
	}

	finalContent := fmt.Sprintf("\n[%s]: %s", match.SkillID, result.Output)
	out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: finalContent}

	e.ChatRepo.SaveMessage(&types.Message{
		ID:             uuid.New().String(),
		ConversationID: conversationID,
		Role:           "assistant",
		Content:        strings.TrimSpace(finalContent),
		Timestamp:      time.Now(),
	})
}

// ExecutePipeline independently consumes the event queue produced by the parser.
// It aggregates assistant output, triggers tools asynchronously, and records history.
func (e *ExecutionEngine) ExecutePipeline(ctx context.Context, in <-chan types.Event, out chan<- llm.StreamEvent, conversationID string) {
	var assistantText string

	for event := range in {
		switch event.Type {
		case types.EventTypeText:
			if txt, ok := event.Payload.(string); ok {
				assistantText += txt
			}

		case types.EventTypeToolCall:
			if payloadMap, ok := event.Payload.(map[string]string); ok {
				toolP := payloadMap["tool_payload"]
				codeP := payloadMap["code_payload"]
				e.DispatchToolPayload(ctx, out, toolP, codeP, conversationID)
			}

		case types.EventTypeError:
			// Emit generalized error over logs
			if e.Verbose {
				slog.Error("Pipeline generated error event", "error", event.Payload)
			}

		case types.EventTypeDone:
			// Stream finished pushing text events
		}
	}

	// Persist the combined conversation context after queue closes or finishes
	if assistantText != "" {
		e.ChatRepo.SaveMessage(&types.Message{
			ID:             uuid.New().String(),
			ConversationID: conversationID,
			Role:           "assistant",
			Content:        strings.TrimSpace(assistantText),
			Timestamp:      time.Now(),
		})
	}
}

// DispatchToolPayload handles a parsed tool payload emitted by the LLM.
// It routes to built-in meta-skills or installed skills via the runner.
func (e *ExecutionEngine) DispatchToolPayload(
	ctx context.Context,
	out chan<- llm.StreamEvent,
	toolPayload string,
	codePayload string,
	conversationID string,
) {
	rawTool := strings.TrimSpace(toolPayload)
	if e.Verbose {
		slog.Info("Parsing Tool JSON", "payload", rawTool)
	}

	var generic map[string]interface{}
	if err := json.Unmarshal([]byte(rawTool), &generic); err != nil {
		if e.Verbose {
			slog.Warn("Failed to unmarshal tool payload", "error", err)
		}
		return
	}

	skillID, _ := generic["skill_id"].(string)
	if skillID == "" && len(codePayload) > 0 {
		skillID = "vraxter-coder"
	}

	out <- llm.StreamEvent{Type: llm.EventTypeSkillCall, Content: skillID}

	e.ChatRepo.SaveMessage(&types.Message{
		ID:             uuid.New().String(),
		ConversationID: conversationID,
		Role:           "skill",
		Content:        fmt.Sprintf("Invoked %s", skillID),
		Timestamp:      time.Now(),
	})

	switch skillID {
	case "vraxter-create-specialist":
		e.handleCreateSpecialist(out, generic, conversationID)

	case "vraxter-delegate":
		specID := ""
		if params, ok := generic["params"].(map[string]interface{}); ok {
			if s, ok := params["specialist_id"].(string); ok {
				specID = s
			}
		}
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n🔄 Delegating query to Specialist %s...", specID)}
		out <- llm.StreamEvent{Type: llm.EventTypeDone}

	case "vraxter-return-control":
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n🔄 Returning control to Main Supervisor..."}
		out <- llm.StreamEvent{Type: llm.EventTypeDone}

	case "vraxter-coder", "vraxter-coder-rust":
		if len(codePayload) > 0 {
			e.handleCoderSkill(ctx, out, generic, codePayload, skillID, conversationID)
		}

	default:
		e.handleStandardSkill(ctx, out, generic, skillID)
	}
}

// --- Internal handlers ---

func (e *ExecutionEngine) handleCreateSpecialist(out chan<- llm.StreamEvent, generic map[string]interface{}, conversationID string) {
	name, expertise, modelID := "", "", ""
	if params, ok := generic["params"].(map[string]interface{}); ok {
		if n, ok := params["name"].(string); ok {
			name = n
		}
		if ex, ok := params["expertise"].(string); ok {
			expertise = ex
		}
		if m, ok := params["model_id"].(string); ok {
			modelID = m
		}
	}
	if name != "" && expertise != "" {
		s := types.Specialist{
			ID:        uuid.New().String(),
			Name:      name,
			Expertise: expertise,
			ModelID:   modelID,
		}
		e.SpecRepo.CreateSpecialist(s)
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n✅ Sub-Agent Specialist '%s' created! (ID: %s, Model: %s)", name, s.ID, modelID)}
	} else {
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n❌ Error: specific 'name' and 'expertise' required to create specialist."}
	}
	out <- llm.StreamEvent{Type: llm.EventTypeDone}
}

func (e *ExecutionEngine) handleCoderSkill(ctx context.Context, out chan<- llm.StreamEvent, generic map[string]interface{}, codePayload, skillID, conversationID string) {
	name, desc := "auto-tool", "Auto-generated skill"

	if params, ok := generic["params"].(map[string]interface{}); ok {
		if n, ok := params["name"].(string); ok {
			name = n
		}
		if d, ok := params["description"].(string); ok {
			desc = d
		}
	} else {
		if n, ok := generic["name"].(string); ok {
			name = n
		} else if n, ok := generic["tool_name"].(string); ok {
			name = n
		}
		if d, ok := generic["description"].(string); ok {
			desc = d
		}
	}

	lang := "go"
	if skillID == "vraxter-coder-rust" {
		lang = "rust"
	}

	if e.Verbose {
		slog.Info("Invoking Sandbox Coder", "lang", lang, "skill", name, "bytes", len(codePayload))
	}
	err := e.Coder.CreateSkill(lang, name, desc, codePayload)

	if err != nil {
		if e.Verbose {
			slog.Error("Coder Compilation Error", "error", err)
		}
		
		// Wrap err into a standardized reproducible engine error
		engineErr := vraxerror.New(vraxerror.ErrTypeSandbox, "Skill compilation/testing failed", true, err)
		errMsg := fmt.Sprintf("\n❌ Code Generation Error: %s\n\n[RECOVERY PROTOCOL: Please fix the issues and retry.]", engineErr.Error())
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: errMsg}
	} else {
		if e.Verbose {
			slog.Info("Skill successfully compiled and installed", "skill", name)
		}
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n✅ Skill '%s' created and installed successfully!", name)}
	}
	out <- llm.StreamEvent{Type: llm.EventTypeDone}
}

func (e *ExecutionEngine) handleStandardSkill(ctx context.Context, out chan<- llm.StreamEvent, generic map[string]interface{}, skillID string) {
	manifest, err := e.Registry.FindSkill(skillID)
	if err != nil {
		return
	}
	params, _ := generic["params"].(map[string]interface{})
	result, err := e.Runner.Execute(ctx, *manifest, params)
	if err == nil {
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n[%s]: %s", skillID, result.Output)}
	}
}
