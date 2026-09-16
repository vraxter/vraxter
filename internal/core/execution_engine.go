// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

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
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/internal/telemetry"
	"github.com/vraxter/vraxter/pkg/types"
	"github.com/vraxter/vraxter/pkg/vraxerror"
)

// ExecutionEngine is responsible for running skills (both LLM-directed and fast-path),
// logging execution events, and emitting result events to the output channel.
type ExecutionEngine struct {
	Registry      *skills.Registry
	Runner        *skills.Runner
	Coder         *services.CoderService
	ChatRepo      *db.ChatRepository
	SpecRepo      *db.SpecialistRepository
	JobManager    *JobManager
	CodeGen       *CodeGenService
	Verbose       bool
	SwarmDelegate func(ctx context.Context, out chan<- llm.StreamEvent, specialistID string, task string, threadID string) string
	ActivateModelDelegate func(sessionID, modelID string) error
	RestoreModelDelegate  func(sessionID string)
	ResolveSpecialistDelegate func(ctx context.Context, query string) (*types.Specialist, error)
	GetActiveModelConfigDelegate func(sessionID string) (types.ModelConfig, error)
	RequireSkillApproval bool
}

// NewExecutionEngine creates a new ExecutionEngine.
func NewExecutionEngine(
	registry *skills.Registry,
	runner *skills.Runner,
	coder *services.CoderService,
	chatRepo *db.ChatRepository,
	specRepo *db.SpecialistRepository,
	jobManager *JobManager,
	codegen *CodeGenService,
	verbose bool,
	requireSkillApproval bool,
) *ExecutionEngine {
	return &ExecutionEngine{
		Registry: registry,
		Runner:   runner,
		Coder:    coder,
		ChatRepo:   chatRepo,
		SpecRepo:   specRepo,
		JobManager: jobManager,
		CodeGen:    codegen,
		Verbose:  verbose,
		RequireSkillApproval: requireSkillApproval,
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
					// Persist intermediate text before breaking off for retry or yield
					if assistantText != "" {
						e.ChatRepo.SaveMessage(&types.Message{
							ID:             uuid.New().String(),
							ConversationID: conversationID,
							Role:           "assistant",
							Content:        strings.TrimSpace(assistantText),
							Timestamp:      time.Now(),
						})
					}

					if feedback == "!YIELD_FOR_APPROVAL" {
						return feedback
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
	
	// Sanitization: Strip markdown backticks if the LLM hallucinated them
	if strings.HasPrefix(rawTool, "```") {
		lines := strings.Split(rawTool, "\n")
		if len(lines) > 2 {
			rawTool = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}
	rawTool = strings.TrimSpace(rawTool)

	if e.Verbose {
		slog.Info("Parsing Tool JSON", "payload", rawTool)
	}

	var call types.ToolCall
	if err := json.Unmarshal([]byte(rawTool), &call); err != nil {
		if len(codePayload) > 0 {
			errMsg := "\n❌ Invalid Tool Payload: You provided source code but failed to invoke the 'vraxter-coder' tool correctly."
			e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: errMsg})
			return "SYSTEM ERROR: You provided source code but failed to invoke the 'vraxter-coder' tool. You MUST output a valid JSON [VRAX_TOOL] block containing the skill 'name' and 'description' parameters before providing the [VRAX_CODE] block."
		} else {
			if e.Verbose {
				slog.Warn("Failed to unmarshal tool payload", "error", err)
			}
			errMsg := fmt.Sprintf("\n❌ Invalid Tool Payload: Could not parse JSON. %v", err)
			e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: errMsg})
			return fmt.Sprintf("SYSTEM ERROR: Tool invocation failed because the JSON payload was invalid. Error: %v. Please re-generate the tool call using strictly valid JSON wrapped IMMEDIATELY AFTER the [VRAX_TOOL] marker. Example: [VRAX_TOOL] {\"skill_id\": \"...\", \"params\": {...}}", err)
		}
	}

	skillID := call.SkillID

	if skillID == "INVALID_EMPTY_PAYLOAD" {
		errMsg := "\n❌ Invalid Tool Payload: You generated the [VRAX_TOOL] marker but provided no valid JSON payload block. Please ensure you output valid JSON."
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: errMsg})
		return "SYSTEM ERROR: Tool invocation failed because the JSON payload was completely missing. Please re-generate the tool call providing the strictly valid JSON block containing the 'skill_id' IMMEDIATELY AFTER the [VRAX_TOOL] marker. Example: [VRAX_TOOL] {\"skill_id\": \"...\", \"params\": {...}}"
	}

	if skillID == "" && len(codePayload) > 0 {
		errMsg := "\n❌ Invalid Tool Payload: Missing skill_id. You MUST output a valid JSON [VRAX_TOOL] block containing the skill 'name' and 'description' parameters before providing the [VRAX_CODE] block."
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: errMsg})
		return "SYSTEM ERROR: Tool invocation failed because the 'skill_id' was missing. You provided source code but failed to invoke the 'vraxter-coder' tool properly. You MUST output a valid JSON [VRAX_TOOL] block containing the skill 'name' and 'description' parameters before providing the [VRAX_CODE] block."
	}

	if len(codePayload) > 0 && skillID != "vraxter-coder" && skillID != "vraxter-coder-rust" {
		errMsg := fmt.Sprintf("\n❌ Invalid Tool Payload: You provided source code but invoked '%s' instead of 'vraxter-coder'.", skillID)
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: errMsg})
		return fmt.Sprintf("SYSTEM ERROR: Tool invocation failed. You provided [VRAX_CODE] but invoked '%s'. To create a skill, you MUST set \"skill_id\": \"vraxter-coder\" in the [VRAX_TOOL] block. Do not put the new skill's name in the skill_id field. Put it in the 'name' parameter instead.", skillID)
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
		return handler(ctx, call, codePayload, conversationID, e, out)
	}

	// Fallback to standard WASM/binary skill runner
	return e.HandleStandardSkill(ctx, out, call, skillID)
}

// --- Internal handlers ---

func (e *ExecutionEngine) handleCreateSpecialist(ctx context.Context, out chan<- llm.StreamEvent, call types.ToolCall, conversationID string) string {
	name, _ := call.Params["name"].(string)
	expertise, _ := call.Params["expertise"].(string)
	modelID, _ := call.Params["model_id"].(string)

	if name != "" && expertise != "" {
		s := types.Specialist{
			ID:        uuid.New().String(),
			Name:      name,
			Expertise: expertise,
			ModelID:   modelID,
		}
		if err := e.SpecRepo.CreateSpecialist(s); err != nil {
			errMsg := fmt.Sprintf("\n❌ Database Error: Failed to create specialist '%s': %v", name, err)
			e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: errMsg})
			return fmt.Sprintf("SYSTEM ERROR: Failed to create specialist in the database. Details: %v", err)
		}
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n✅ Sub-Agent Specialist '%s' created! (ID: %s, Model: %s)", name, s.ID, modelID)})
		return fmt.Sprintf("SYSTEM: Successfully created specialist '%s'. You may now converse with the user and let them know.", name)
	} else {
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n❌ Error: specific 'name' and 'expertise' required to create specialist."})
		return "SYSTEM ERROR: You must provide a valid 'name' and 'expertise' string parameters to create a specialist."
	}
}

func (e *ExecutionEngine) handleCoderSkill(ctx context.Context, out chan<- llm.StreamEvent, call types.ToolCall, codePayload, skillID, conversationID string) string {
	name, desc := "auto-tool", "Auto-generated skill"

	if n, ok := call.Params["name"].(string); ok {
		name = n
	}
	if d, ok := call.Params["description"].(string); ok {
		desc = d
	}

	lang := "go"
	if skillID == "vraxter-coder-rust" {
		lang = "rust"
	}

	if e.Verbose {
		slog.Info("Invoking Sandbox Coder", "lang", lang, "skill", name, "bytes", len(codePayload))
	}
	err := e.Coder.CreateSkill(lang, name, desc, "", codePayload, nil)

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

func (e *ExecutionEngine) HandleStandardSkill(ctx context.Context, out chan<- llm.StreamEvent, call types.ToolCall, skillID string) string {
	manifest, err := e.Registry.FindSkill(skillID)
	if err != nil {
		return fmt.Sprintf("ERROR: Skill %s not found in registry.", skillID)
	}
	
	if call.IsDaemon {
		jobID := e.JobManager.SpawnDaemon(fmt.Sprintf("Skill %s", skillID), func() (string, error) {
			// Create a background context that survives the parent stream closure
			bgCtx := context.Background()
			res, err := e.Runner.Execute(bgCtx, *manifest, call.Params)
			if err != nil {
				return "", err
			}
			return res.Output, nil
		})

		msg := fmt.Sprintf("\n⚡ Spawned Daemon Job for '%s' (Job ID: %s)", skillID, jobID)
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: msg})
		return fmt.Sprintf("DAEMON SPAWNED: Skill '%s' is now running in the background with Job ID: %s. You will be notified when it completes.", skillID, jobID)
	}

	if e.RequireSkillApproval {
		payloadBytes, _ := json.Marshal(call)
		e.emitEvent(ctx, out, llm.StreamEvent{
			Type:    llm.EventTypeSkillApprovalRequest,
			Content: string(payloadBytes),
			SkillID: skillID,
		})
		return "!YIELD_FOR_APPROVAL" // Magic string intercepted by the ExecutionEngine
	}

	result, err := e.Runner.Execute(ctx, *manifest, call.Params)
	if err != nil {
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n❌ Skill execution failed: %v", err)})
		
		schemaHint := ""
		if manifest.ParamsSchema != "" {
			schemaHint = fmt.Sprintf("\nREQUIRED SCHEMA: %s\n\nCRITICAL DIRECTIVE: The tool execution failed. Compare your previous payload against the REQUIRED SCHEMA above, fix the mismatch, and RE-INVOKE the tool. Do NOT stop.", manifest.ParamsSchema)
		}
		return fmt.Sprintf("SKILL ['%s'] FAILED: %v%s", skillID, err, schemaHint)
	}
	
	e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n[%s]: %s", skillID, result.Output)})
	return fmt.Sprintf("SKILL ['%s'] RETURNED RESULT: %s", skillID, result.Output)
}
