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
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
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
	"vraxter-see":               handleCmdSee,
	"vraxter-hear":              handleCmdHear,
	"vraxter-talk":              handleCmdTalk,
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

func handleCmdSee(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	path, _ := call.Params["path"].(string)
	prompt, _ := call.Params["prompt"].(string)
	if prompt == "" {
		prompt = "Describe this image."
	}
	if path == "" {
		return "ERROR: Missing required parameter 'path'."
	}

	resolvedPath := skills.ResolveMountPath(path)
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to read image file at '%s': %v", path, err)
	}

	if e.GetActiveModelConfigDelegate == nil {
		return "ERROR: Active model config delegate is not initialized."
	}
	activeCfg, err := e.GetActiveModelConfigDelegate(conversationID)
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to retrieve active model config: %v", err)
	}

	hasVision := false
	for _, c := range strings.Split(strings.ToLower(activeCfg.Capabilities), ",") {
		c = strings.TrimSpace(c)
		if c == "vision" || c == "image" {
			hasVision = true
			break
		}
	}
	if !hasVision {
		return fmt.Sprintf("ERROR: Active model '%s' (%s) does not support vision capabilities. Please configure or activate a vision-capable model.", activeCfg.Alias, activeCfg.Model)
	}

	mimeType := "image/jpeg"
	ext := strings.ToLower(filepath.Ext(resolvedPath))
	switch ext {
	case ".png":
		mimeType = "image/png"
	case ".webp":
		mimeType = "image/webp"
	case ".gif":
		mimeType = "image/gif"
	}

	b64Data := base64.StdEncoding.EncodeToString(data)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, b64Data)

	_, provider, err := e.CodeGen.Router.GetProviderByID(activeCfg.ID)
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to resolve provider for model '%s': %v", activeCfg.Model, err)
	}

	e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeStatus, Content: fmt.Sprintf("👀 Analyzing image with model '%s'...", activeCfg.Alias)})

	req := interfaces.CompletionRequest{
		Model: activeCfg.Model,
		Messages: []interfaces.Message{
			{Role: "system", Content: "You are a helpful visual assistant. Answer questions or describe images accurately based on user prompts."},
			{Role: "user", Content: fmt.Sprintf("%s %s", dataURI, prompt)},
		},
	}

	resp, err := provider.Generate(ctx, req)
	if err != nil {
		return fmt.Sprintf("ERROR: Image analysis failed: %v", err)
	}

	return fmt.Sprintf("IMAGE ANALYSIS RESULT:\n%s", resp.Content)
}

func handleCmdHear(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	path, _ := call.Params["path"].(string)
	prompt, _ := call.Params["prompt"].(string)
	if prompt == "" {
		prompt = "Transcribe the following audio exactly. Do not add any commentary."
	}
	if path == "" {
		return "ERROR: Missing required parameter 'path'."
	}

	resolvedPath := skills.ResolveMountPath(path)
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to read audio file at '%s': %v", path, err)
	}

	if e.GetActiveModelConfigDelegate == nil {
		return "ERROR: Active model config delegate is not initialized."
	}
	activeCfg, err := e.GetActiveModelConfigDelegate(conversationID)
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to retrieve active model config: %v", err)
	}

	_, provider, err := e.CodeGen.Router.GetProviderByID(activeCfg.ID)
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to resolve provider for model '%s': %v", activeCfg.Model, err)
	}

	mimeType := "audio/wav"
	ext := strings.ToLower(filepath.Ext(resolvedPath))
	switch ext {
	case ".mp3":
		mimeType = "audio/mp3"
	case ".ogg":
		mimeType = "audio/ogg"
	case ".m4a":
		mimeType = "audio/m4a"
	case ".flac":
		mimeType = "audio/flac"
	}

	if transcriber, ok := provider.(interfaces.AudioTranscriber); ok {
		e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeStatus, Content: fmt.Sprintf("👂 Transcribing audio via API '%s'...", activeCfg.Alias)})
		text, err := transcriber.TranscribeAudio(ctx, data, mimeType, prompt)
		if err != nil {
			return fmt.Sprintf("ERROR: Transcription API failed: %v", err)
		}
		return fmt.Sprintf("AUDIO TRANSCRIPTION RESULT:\n%s", text)
	}

	hasAudio := false
	for _, c := range strings.Split(strings.ToLower(activeCfg.Capabilities), ",") {
		c = strings.TrimSpace(c)
		if c == "audio" || c == "voice" {
			hasAudio = true
			break
		}
	}
	if !hasAudio {
		return fmt.Sprintf("ERROR: Active model '%s' (%s) does not support audio processing capabilities.", activeCfg.Alias, activeCfg.Model)
	}

	b64Data := base64.StdEncoding.EncodeToString(data)
	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, b64Data)

	e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeStatus, Content: fmt.Sprintf("👂 Transcribing audio with model '%s'...", activeCfg.Alias)})

	req := interfaces.CompletionRequest{
		Model: activeCfg.Model,
		Messages: []interfaces.Message{
			{Role: "system", Content: "You are a helpful assistant. You transcribe or analyze audio content accurately."},
			{Role: "user", Content: fmt.Sprintf("%s %s", dataURI, prompt)},
		},
	}

	resp, err := provider.Generate(ctx, req)
	if err != nil {
		return fmt.Sprintf("ERROR: Audio analysis failed: %v", err)
	}

	return fmt.Sprintf("AUDIO TRANSCRIPTION RESULT:\n%s", resp.Content)
}

func handleCmdTalk(ctx context.Context, call types.ToolCall, codePayload, conversationID string, e *ExecutionEngine, out chan<- llm.StreamEvent) string {
	text, _ := call.Params["text"].(string)
	targetZone, _ := call.Params["target_zone"].(string)
	voice, _ := call.Params["voice"].(string)
	if voice == "" {
		voice = "alloy"
	}

	if text == "" {
		return "ERROR: Missing required parameter: 'text'."
	}

	if e.GetActiveModelConfigDelegate == nil {
		return "ERROR: Active model config delegate is not initialized."
	}
	activeCfg, err := e.GetActiveModelConfigDelegate(conversationID)
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to retrieve active model config: %v", err)
	}

	_, provider, err := e.CodeGen.Router.GetProviderByID(activeCfg.ID)
	if err != nil {
		return fmt.Sprintf("ERROR: Failed to resolve provider for model '%s': %v", activeCfg.Model, err)
	}

	synth, ok := provider.(interfaces.SpeechSynthesizer)
	if !ok {
		return fmt.Sprintf("ERROR: Speech synthesis (talk) is not supported by the active provider '%s' (%s). Please switch to an OpenAI-compatible provider that supports /v1/audio/speech endpoints.", activeCfg.Provider, activeCfg.Model)
	}

	e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeStatus, Content: fmt.Sprintf("🗣️ Synthesizing speech with model '%s'...", activeCfg.Alias)})

	audioBytes, err := synth.SynthesizeSpeech(ctx, text, voice)
	if err != nil {
		return fmt.Sprintf("ERROR: Speech synthesis failed: %v", err)
	}

	b64Audio := base64.StdEncoding.EncodeToString(audioBytes)
	
	// Create JSON playback payload
	payloadJSON := fmt.Sprintf(`{"action":"playback","target_zone":"%s","audio_base64":"%s"}`, targetZone, b64Audio)
	
	// Push over the stream as a special Status event so the UI/Client can catch it and handle playback
	out <- llm.StreamEvent{
		Type:    llm.EventTypeStatus,
		Content: fmt.Sprintf("[AUDIO_PLAYBACK_EVENT]%s", payloadJSON),
	}

	e.emitEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n🔊 Synthesized speech routed to zone '%s'\n", targetZone)})
	return fmt.Sprintf("SUCCESS: Synthesized speech routed to zone '%s'.", targetZone)
}
