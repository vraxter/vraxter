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
	"fmt"
	"log/slog"
	"strings"

	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/prompts"
	"github.com/vraxter/vraxter/internal/services"
)

// CodeGenService encapsulates the two-phase skill creation pipeline.
// It is client-agnostic: communicates ONLY through chan<- llm.StreamEvent.
// TUI, CLI, web UI, and API consumers all receive the same lifecycle events.
type CodeGenService struct {
	Router  *llm.Router
	Coder   *services.CoderService
	Verbose bool
}

// GenerateAndCompile takes a skill spec and produces a compiled WASM binary.
// It resolves the coding model from routing.yaml, selects the correct
// boilerplate (Go or Rust), constructs a focused code-gen prompt, and compiles.
func (s *CodeGenService) GenerateAndCompile(
	ctx context.Context,
	out chan<- llm.StreamEvent,
	name, description, spec, lang string,
) error {
	if lang == "" {
		lang = "go"
	}

	// Resolve the coding model via routing.yaml → fallback to priority order
	modelCfg, provider, err := s.Router.GetProviderForUseCase("coding")
	if err != nil {
		return fmt.Errorf("no model available for code generation: %w", err)
	}

	if s.Verbose {
		slog.Info("CodeGen: resolved model", "model", modelCfg.Model, "lang", lang, "skill", name)
	}

	// Render the code-gen system prompt
	systemPrompt, err := prompts.RenderCodeGen(prompts.CodeGenParams{
		Language:    lang,
		Name:        name,
		Description: description,
		Spec:        spec,
	})
	if err != nil {
		return fmt.Errorf("failed to render codegen prompt: %w", err)
	}

	maxRetries := 3
	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Emit lifecycle progress
		if attempt == 0 {
			msg := fmt.Sprintf("⚙️ Generating %s code for '%s'...", strings.Title(lang), name)
			s.emit(ctx, out, llm.EventTypeStatus, msg)
			s.emit(ctx, out, llm.EventTypeToken, "\n"+msg+"\n")
		} else {
			msg := fmt.Sprintf("🔄 Retrying code generation (attempt %d/%d)...", attempt+1, maxRetries)
			s.emit(ctx, out, llm.EventTypeStatus, msg)
			s.emit(ctx, out, llm.EventTypeToken, "\n"+msg+"\n")
		}

		// Build the messages for the code-gen LLM call
		msgs := []llm.Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: fmt.Sprintf("Generate the complete %s source code for the '%s' skill. Requirements: %s", lang, name, spec)},
		}

		// Inject error context from previous failed attempt
		if lastErr != nil {
			msgs = append(msgs, llm.Message{
				Role:    "user",
				Content: fmt.Sprintf("COMPILATION ERROR FROM PREVIOUS ATTEMPT:\n%s\n\nFix the error and regenerate the COMPLETE source code.", lastErr.Error()),
			})
		}

		req := llm.CompletionRequest{
			Model:    modelCfg.Model,
			Messages: msgs,
		}

		// Non-streaming call — we don't show raw code to the user
		resp, err := provider.Generate(ctx, req)
		if err != nil {
			lastErr = fmt.Errorf("LLM generation failed: %w", err)
			if s.Verbose {
				slog.Warn("CodeGen: LLM call failed", "attempt", attempt+1, "error", err)
			}
			continue
		}

		content := strings.TrimSpace(resp.Content)
		if content == "" {
			lastErr = fmt.Errorf("LLM returned empty response")
			continue
		}

		// Parse Schema and Code blocks
		var permissions []string
		schema := ""
		code := ""
		
		content = strings.TrimSpace(content)
		codeIdx := strings.Index(content, "[VRAX_CODE]")
		if codeIdx != -1 {
			code = strings.TrimSpace(content[codeIdx+len("[VRAX_CODE]"):])
			content = content[:codeIdx]
		} else {
			// Absolute fallback for legacy models or unexpected formatting
			code = content
			if strings.HasPrefix(code, "```") {
				// Strip markdown if they ignored the rules
				lines := strings.Split(code, "\n")
				if len(lines) > 2 {
					code = strings.Join(lines[1:len(lines)-1], "\n")
				}
			}
			content = ""
		}

		schemaIdx := strings.Index(content, "[VRAX_SCHEMA]")
		if schemaIdx != -1 {
			schema = strings.TrimSpace(content[schemaIdx+len("[VRAX_SCHEMA]"):])
			content = content[:schemaIdx]
		} else if strings.Contains(content, "{") && strings.Contains(content, "}") {
			start := strings.Index(content, "{")
			end := strings.LastIndex(content, "}")
			if start != -1 && end != -1 && end > start {
				schema = strings.TrimSpace(content[start : end+1])
			}
		}

		permIdx := strings.Index(content, "[VRAX_PERMISSIONS]")
		if permIdx != -1 {
			permStr := strings.TrimSpace(content[permIdx+len("[VRAX_PERMISSIONS]"):])
			if permStr != "" && strings.ToLower(permStr) != "none" {
				for _, p := range strings.Split(permStr, ",") {
					pClean := strings.TrimSpace(p)
					if pClean != "" {
						permissions = append(permissions, pClean)
					}
				}
			}
		}

		if s.Verbose {
			slog.Info("CodeGen: received blocks", "codeBytes", len(code), "schema", schema, "permissions", permissions, "attempt", attempt+1)
		}

		// Phase 2: Compile
		s.emit(ctx, out, llm.EventTypeStatus, "🔨 Compiling and verifying skill...")
		s.emit(ctx, out, llm.EventTypeToken, "🔨 Compiling and verifying skill...\n")

		compileErr := s.Coder.CreateSkill(lang, name, description, schema, code, permissions)
		if compileErr != nil {
			lastErr = compileErr
			if s.Verbose {
				slog.Warn("CodeGen: compilation failed", "attempt", attempt+1, "error", compileErr)
			}
			s.emit(ctx, out, llm.EventTypeToken,
				fmt.Sprintf("\n❌ Compilation failed (attempt %d/%d): %v", attempt+1, maxRetries, compileErr))
			continue
		}

		// Success!
		s.emit(ctx, out, llm.EventTypeToken,
			fmt.Sprintf("\n✅ Skill '%s' installed successfully!", name))
		return nil
	}

	// All retries exhausted
	s.emit(ctx, out, llm.EventTypeToken,
		fmt.Sprintf("\n❌ Skill creation failed after %d attempts: %v", maxRetries, lastErr))
	return fmt.Errorf("skill creation failed after %d attempts: %w", maxRetries, lastErr)
}

func (s *CodeGenService) emit(ctx context.Context, out chan<- llm.StreamEvent, eventType llm.StreamEventType, content string) {
	select {
	case out <- llm.StreamEvent{Type: eventType, Content: content}:
	case <-ctx.Done():
	}
}
