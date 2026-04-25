package core

import (
	"context"
	"fmt"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/skills"
)

// InternalToolHandler defines the signature for built-in executable tools
type InternalToolHandler func(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string

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

func handleCmdCreateSpecialist(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	e.handleCreateSpecialist(ctx, out, generic, conversationID)
	return ""
}

func handleCmdActivateModel(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	if params, ok := generic["params"].(map[string]interface{}); ok {
		if modelID, ok := params["model_id"].(string); ok {
			if e.ActivateModelDelegate != nil {
				if err := e.ActivateModelDelegate(conversationID, modelID); err != nil {
					e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeError, Content: fmt.Sprintf("❌ Activation failed: %v", err)})
					return fmt.Sprintf("SYSTEM ERROR: Model '%s' is not recognized or available in your current configuration.", modelID)
				}
				e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeStatus, Content: fmt.Sprintf("🎯 **%s** is now locked for this session.", modelID)})
				return fmt.Sprintf("🚀 Vraxter is now locked to **%s** for this conversation. Automatic routing is suspended until you restore it.", modelID)
			}
		}
	}
	return "SYSTEM ERROR: Failed to activate model. Please provide a valid model ID or identifier."
}

func handleCmdRestoreModel(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	if e.RestoreModelDelegate != nil {
		e.RestoreModelDelegate(conversationID)
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeStatus, Content: "🔄 Automatic routing restored."})
		return "✅ Vraxter model routing has been restored to **Automatic**. I will now select models based on your intent again."
	}
	return "SYSTEM ERROR: Failed to restore model routing."
}

func handleCmdDelegate(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	specID, taskText := "", ""
	if params, ok := generic["params"].(map[string]interface{}); ok {
		if s, ok := params["specialist_id"].(string); ok {
			specID = s
		}
		if t, ok := params["task"].(string); ok {
			taskText = t
		}
	}

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

func handleCmdReturnControl(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	return "CONTROL RETURNED. The specialist sub-agent has exited the conversation. SYSTEM DIRECTIVE: YOU ARE NOW 'VRAXTER' (The primary Orchestrator). STOP ACTING LIKE THE SPECIALIST. Abandon all sub-agent personas. You MUST now answer the user's latest query using your full capabilities as Vraxter."
}

func handleCmdCoder(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	skillID, _ := generic["skill_id"].(string)
	if skillID == "" { // Fallback context
		skillID = "vraxter-coder"
	}
	if len(codePayload) > 0 {
		return e.handleCoderSkill(ctx, out, generic, codePayload, skillID, conversationID)
	}
	return ""
}

func handleCmdReadFile(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	path, _ := generic["path"].(string)
	return skills.ReadFile(path)
}

func handleCmdListDir(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	path, _ := generic["path"].(string)
	return skills.ListDir(path)
}

func handleCmdPatchCode(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	path, _ := generic["path"].(string)
	search, _ := generic["search"].(string)
	replace, _ := generic["replace"].(string)
	return skills.PatchCode(path, search, replace)
}

func handleCmdDeleteSpecialist(ctx context.Context, generic map[string]interface{}, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	identifier := ""
	if params, ok := generic["params"].(map[string]interface{}); ok {
		if idt, ok := params["identifier"].(string); ok {
			identifier = idt
		}
	} else if idt, ok := generic["identifier"].(string); ok {
		identifier = idt
	}

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
