package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/internal/telemetry"
	"github.com/patagonicrune/vraxter/pkg/types"
	"github.com/patagonicrune/vraxter/pkg/vraxerror"
)

// ExecutionEngine is responsible for running skills (both LLM-directed and fast-path),
// logging execution events, and emitting result events to the output channel.
type ExecutionEngine struct {
	Registry      *skills.Registry
	Runner        *skills.Runner
	Coder         *services.CoderService
	ChatRepo      *db.ChatRepository
	SpecRepo      *db.SpecialistRepository
	Verbose       bool
	SwarmDelegate func(ctx context.Context, out chan<- llm.StreamEvent, specialistID string, task string, threadID string) string
	ActivateModelDelegate func(sessionID, modelID string) error
	RestoreModelDelegate  func(sessionID string)
	ResolveSpecialistDelegate func(ctx context.Context, query string) (*types.Specialist, error)
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

// emitEvent safely sends an event to the out channel, aborting if context is canceled.
func (e *ExecutionEngine) emitEvent(ctx context.Context, out chan<- llm.StreamEvent, event llm.StreamEvent) {
	select {
	case out <- event:
	case <-ctx.Done():
	}
}

// RunFastPath executes a pre-resolved skill match without consulting the LLM.
// It emits a SkillCall event, runs the skill, and emits a Token event with the result.
func (e *ExecutionEngine) RunFastPath(ctx context.Context, out chan<- llm.StreamEvent, match types.IntentMatch, conversationID string) {
	e.ChatRepo.SaveMessage(&types.Message{
		ID:             uuid.New().String(),
		ConversationID: conversationID,
		Role:           match.Type,
		Content:        fmt.Sprintf("Invoked %s (Fast Path)", match.ID),
		Timestamp:      time.Now(),
	})

	var finalContent string

	switch match.Type {
	case types.IntentTypeSkill:
		// We don't emit EventTypeSkillCall here because DispatchToolPayload will do it consistently
		manifest, err := e.Registry.FindSkill(match.ID)
		if err != nil {
			out <- llm.StreamEvent{
				Type:    llm.EventTypeToken,
				Content: fmt.Sprintf("\n❌ Skill not found: %s", match.ID),
			}
			return
		}

		// NATIVE SKILLS BYPASS: If it's a built-in manager tool, use the internal dispatcher
		if strings.HasPrefix(match.ID, "vraxter-") {
			payloadJSON, _ := json.Marshal(map[string]interface{}{
				"skill_id": match.ID,
				"params":   match.Params,
			})
			if feedback := e.DispatchToolPayload(ctx, out, string(payloadJSON), "", conversationID); feedback != "" {
				out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n" + feedback}
				return
			}
		}

		result, err := e.Runner.Execute(ctx, *manifest, match.Params)
		if err != nil {
			out <- llm.StreamEvent{
				Type:    llm.EventTypeToken,
				Content: fmt.Sprintf("\n❌ Skill execution failed: %v", err),
			}
			return
		}

		finalContent = fmt.Sprintf("\n[%s]: %s", match.ID, result.Output)
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: finalContent}
	case types.IntentTypeCommand:
		out <- llm.StreamEvent{Type: llm.EventTypeCommandCall, Content: match.ID}
		cmd := exec.CommandContext(ctx, match.ID, match.Args...)
		output, err := cmd.CombinedOutput()
		if err != nil {
			out <- llm.StreamEvent{
				Type:    llm.EventTypeToken,
				Content: fmt.Sprintf("\n❌ Command execution failed: %v", err),
			}
			return
		}

		finalContent = fmt.Sprintf("\n[%s]: %s", match.ID, string(output))
		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: finalContent}
	case types.IntentTypeSpecialist:
		displayName := match.ID
		spec, err := e.SpecRepo.GetSpecialist(match.ID)
		if err == nil && spec != nil {
			displayName = spec.Name
		}

		out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n🔄 Sub-Agent Swarm Spawned: Delegating task to '%s' (Fast Path)...\n", displayName)}

		if e.SwarmDelegate != nil {
			taskText, _ := match.Params["task"].(string)
			swarmResult := e.SwarmDelegate(ctx, out, match.ID, taskText, conversationID)
			
			// ◈ SEMANTIC PROTOCOL: Sub-agent result is now STREAMED natively.
			// We send a metadata-only event to ensure the TUI latches the ID, but we do NOT repeat the content.
			out <- llm.StreamEvent{
				Type:    llm.EventTypeSpecialistResult, 
				Content: fmt.Sprintf("%s|%s|", match.ID, displayName),
			}
			finalContent = swarmResult
		} else {
			finalContent = "\n❌ Error: SwarmDelegate not fully bound in engine."
			out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: finalContent}
		}
	}

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
func (e *ExecutionEngine) ExecutePipeline(ctx context.Context, in <-chan types.Event, out chan<- llm.StreamEvent, conversationID string) string {
	ctx, span := telemetry.StartSpan(ctx, "engine.ExecutePipeline")
	defer span.End()

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
				if feedback := e.DispatchToolPayload(ctx, out, toolP, codeP, conversationID); feedback != "" {
					// Persist intermediate text before breaking off for retry
					if assistantText != "" {
						e.ChatRepo.SaveMessage(&types.Message{
							ID:             uuid.New().String(),
							ConversationID: conversationID,
							Role:           "assistant",
							Content:        strings.TrimSpace(assistantText),
							Timestamp:      time.Now(),
						})
					}
					return feedback
				}
			}

		case types.EventTypeError:
			if err, ok := event.Payload.(error); ok {
				e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeError, Err: err})
			}
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

	return ""
}

// DispatchToolPayload handles a parsed tool payload emitted by the LLM.
// It routes to built-in meta-skills or installed skills via the runner.
func (e *ExecutionEngine) DispatchToolPayload(
	ctx context.Context,
	out chan<- llm.StreamEvent,
	toolPayload string,
	codePayload string,
	conversationID string,
) string {
	rawTool := strings.TrimSpace(toolPayload)
	if e.Verbose {
		slog.Info("Parsing Tool JSON", "payload", rawTool)
	}

	var generic map[string]interface{}
	if err := json.Unmarshal([]byte(rawTool), &generic); err != nil {
		if e.Verbose {
			slog.Warn("Failed to unmarshal tool payload", "error", err)
		}
		return ""
	}

	skillID, _ := generic["skill_id"].(string)
	if skillID == "" && len(codePayload) > 0 {
		skillID = "vraxter-coder"
	}

	e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeSkillCall, Content: skillID})

	e.ChatRepo.SaveMessage(&types.Message{
		ID:             uuid.New().String(),
		ConversationID: conversationID,
		Role:           "skill",
		Content:        fmt.Sprintf("Invoked %s", skillID),
		Timestamp:      time.Now(),
	})

	// Look for a registered internal command
	if handler, exists := BuiltInCommands[skillID]; exists {
		return handler(ctx, generic, codePayload, conversationID, e, out)
	}

	// Fallback to standard WASM/binary skill runner
	return e.handleStandardSkill(ctx, out, generic, skillID)
}

// --- Internal handlers ---

func (e *ExecutionEngine) handleCreateSpecialist(ctx context.Context, out chan<- llm.StreamEvent, generic map[string]interface{}, conversationID string) {
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
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n✅ Sub-Agent Specialist '%s' created! (ID: %s, Model: %s)", name, s.ID, modelID)})
	} else {
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n❌ Error: specific 'name' and 'expertise' required to create specialist."})
	}
}

func (e *ExecutionEngine) handleCoderSkill(ctx context.Context, out chan<- llm.StreamEvent, generic map[string]interface{}, codePayload, skillID, conversationID string) string {
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

		engineErr := vraxerror.New(vraxerror.ErrTypeSandbox, "Skill compilation/testing failed", true, err)
		errMsg := fmt.Sprintf("\n❌ Auto-Tool Iteration Failed: %s\n🔄 Engine retrying autonomously...", engineErr.Error())
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: errMsg})

		return fmt.Sprintf("Code Generation Error:\n%s\n\nPlease review your code block. Fix the compilation errors and RE-GENERATE the entire tool specifically using the 3 VRAXTER PROTOCOL tags ([VRAX_CHAT], [VRAX_TOOL], [VRAX_CODE]) EXACTLY as documented. DO NOT JUST RETURN CODE.", err.Error())
	} else {
		if e.Verbose {
			slog.Info("Skill successfully compiled and installed", "skill", name)
		}
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n✅ Skill '%s' created and installed successfully!", name)})
	}
	return ""
}

func (e *ExecutionEngine) handleStandardSkill(ctx context.Context, out chan<- llm.StreamEvent, generic map[string]interface{}, skillID string) string {
	manifest, err := e.Registry.FindSkill(skillID)
	if err != nil {
		return fmt.Sprintf("ERROR: Skill %s not found in registry.", skillID)
	}
	params, _ := generic["params"].(map[string]interface{})
	result, err := e.Runner.Execute(ctx, *manifest, params)
	if err != nil {
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n❌ Skill execution failed: %v", err)})
		return fmt.Sprintf("SKILL ['%s'] FAILED: %v", skillID, err)
	}
	
	e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n[%s]: %s", skillID, result.Output)})
	return fmt.Sprintf("SKILL ['%s'] RETURNED RESULT: %s", skillID, result.Output)
}
