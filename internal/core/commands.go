package core

import (
	"context"
	"fmt"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/pkg/types"
)

// InternalToolHandler defines the signature for built-in executable tools
type InternalToolHandler func(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string

// BuiltInCommands holds the registry of all native system commands
var BuiltInCommands = map[string]InternalToolHandler{
	"vraxter-create-specialist": handleCmdCreateSpecialist,
	"vraxter-activate-model":    handleCmdActivateModel,
	"vraxter-restore-model":     handleCmdRestoreModel,
	"vraxter-delegate":          handleCmdDelegate,
	"vraxter-return-control":    handleCmdReturnControl,
	"vraxter-coder":             handleCmdCoder,
	"vraxter-coder-rust":        handleCmdCoder, // Shared handler
	"vraxter-read-file":         handleCmdReadFile,
	"vraxter-list-dir":          handleCmdListDir,
	"vraxter-patch-code":        handleCmdPatchCode,
	"vraxter-delete-specialist": handleCmdDeleteSpecialist,
}

func handleCmdCreateSpecialist(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	return e.handleCreateSpecialist(ctx, out, call, conversationID)
}

func handleCmdActivateModel(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	if modelID, ok := call.Params["model_id"].(string); ok {
		if e.ActivateModelDelegate != nil {
			if err := e.ActivateModelDelegate(conversationID, modelID); err != nil {
				e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeError, Content: fmt.Sprintf("❌ Activation failed: %v", err)})
				return fmt.Sprintf("SYSTEM ERROR: Model '%s' is not recognized or available in your current configuration.", modelID)
			}
			e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeStatus, Content: fmt.Sprintf("🎯 **%s** is now locked for this session.", modelID)})
			return fmt.Sprintf("🚀 Vraxter is now locked to **%s** for this conversation. Automatic routing is suspended until you restore it.", modelID)
		}
	}
	return "SYSTEM ERROR: Failed to activate model. Please provide a valid model ID or identifier."
}

func handleCmdRestoreModel(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	if e.RestoreModelDelegate != nil {
		e.RestoreModelDelegate(conversationID)
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeStatus, Content: "🔄 Automatic routing restored."})
		return "✅ Vraxter model routing has been restored to **Automatic**. I will now select models based on your intent again."
	}
	return "SYSTEM ERROR: Failed to restore model routing."
}

func handleCmdDelegate(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	specID, _ := call.Params["specialist_id"].(string)
	taskText, _ := call.Params["task"].(string)

	displayName := specID
	if e.SpecRepo != nil {
		spec, err := e.SpecRepo.GetSpecialist(specID)
		if err == nil && spec != nil {
			displayName = spec.Name
		}
	}

	e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n🔄 Sub-Agent Swarm Spawned: Delegating task to '%s'...\n", displayName)})

	if e.SwarmDelegate != nil {
		swarmResult := e.SwarmDelegate(ctx, out, specID, taskText, conversationID)
		return fmt.Sprintf("AGENT [%s] TASK COMPLETE. Result: %s", specID, swarmResult)
	}
	return "ERROR: SwarmDelegate not fully bound in engine."
}

func handleCmdReturnControl(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	return "CONTROL RETURNED. The specialist sub-agent has exited the conversation. SYSTEM DIRECTIVE: YOU ARE NOW 'VRAXTER' (The primary Orchestrator). STOP ACTING LIKE THE SPECIALIST. Abandon all sub-agent personas. You MUST now answer the user's latest query using your full capabilities as Vraxter."
}

func handleCmdCoder(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	skillID := call.SkillID
	if skillID == "" { // Fallback context
		skillID = "vraxter-coder"
	}

	name, _ := call.Params["name"].(string)
	desc, _ := call.Params["description"].(string)
	spec, _ := call.Params["spec"].(string)
	lang, _ := call.Params["language"].(string)

	if lang == "" {
		lang = "go"
		if skillID == "vraxter-coder-rust" {
			lang = "rust"
		}
	}

	// Auto-recover: if the LLM provided code directly (e.g. via [VRAX_CODE] block or JSON param), use it
	if len(codePayload) > 0 {
		return e.handleCoderSkill(ctx, out, call, codePayload, skillID, conversationID)
	}
	for _, key := range []string{"tool_code", "code", "source"} {
		if code, ok := call.Params[key].(string); ok && len(code) > 0 {
			return e.handleCoderSkill(ctx, out, call, code, skillID, conversationID)
		}
	}

	// Phase 2: CodeGen pipeline
	if spec == "" {
		spec = desc // fallback: use description as spec if spec is missing
	}

	if e.CodeGen == nil {
		return "SYSTEM ERROR: CodeGenService not initialized in engine."
	}

	err := e.CodeGen.GenerateAndCompile(ctx, out, name, desc, spec, lang)
	if err != nil {
		return fmt.Sprintf("SYSTEM ERROR: Failed to generate/compile skill: %v", err)
	}

	// Post-Creation Actions
	if runParams, ok := call.Params["run_params"].(map[string]interface{}); ok {
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n🚀 **Auto-Executing** '%s' to fulfill your request...\n", name)})
		
		// Map parameters and execute
		runCall := types.ToolCall{
			SkillID: name,
			Params:  runParams,
		}
		return e.HandleStandardSkill(ctx, out, runCall, name)
	}

	if promptRun, ok := call.Params["prompt_run"].(bool); ok && promptRun {
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("[PROMPT_RUN:%s]", name)})
		return fmt.Sprintf("SUCCESS: Skill '%s' installed. Prompting user for execution.", name)
	}

	return fmt.Sprintf("SUCCESS: Skill '%s' installed and verified. It is now ready for use.", name)
}

func handleCmdReadFile(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	path, _ := call.Params["path"].(string)
	return skills.ReadFile(path)
}

func handleCmdListDir(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	path, _ := call.Params["path"].(string)
	return skills.ListDir(path)
}

func handleCmdPatchCode(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	path, _ := call.Params["path"].(string)
	search, _ := call.Params["search"].(string)
	replace, _ := call.Params["replace"].(string)
	return skills.PatchCode(path, search, replace)
}

func handleCmdDeleteSpecialist(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	identifier, _ := call.Params["identifier"].(string)

	if identifier == "" {
		return "ERROR: identifier (Name or ID) parameter is required to delete a specialist."
	}

	if e.ResolveSpecialistDelegate != nil {
		spec, err := e.ResolveSpecialistDelegate(ctx, identifier)
		if err != nil {
			return fmt.Sprintf("FAILED TO DELETE: Cannot securely isolate specialist using identifier '%s'. %v", identifier, err)
		}

		err = e.SpecRepo.DeleteSpecialist(spec.ID)
		if err != nil {
			return fmt.Sprintf("FAILED TO DELETE specialist '%s' due to internal database error: %v", spec.Name, err)
		}
		
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n🗑️ Specialist Deleted: **%s** (ID: %s)\n", spec.Name, spec.ID)})
		return fmt.Sprintf("SUCCESS: Specialist '%s' has been permanently retired and deleted from the database.", spec.Name)
	}

	return "ERROR: ResolveSpecialistDelegate not mapped in ExecutionEngine."
}
