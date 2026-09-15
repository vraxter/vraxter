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

	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/telemetry"
	"github.com/patagonicrune/vraxter/pkg/types"
	"github.com/patagonicrune/vraxter/pkg/vraxerror"
)

type StreamCoordinator struct {
	Router  *llm.Router
	Verbose bool
}

func NewStreamCoordinator(router *llm.Router, verbose bool) *StreamCoordinator {
	return &StreamCoordinator{
		Router:  router,
		Verbose: verbose,
	}
}

func (sc *StreamCoordinator) Run(
	ctx context.Context,
	out chan<- llm.StreamEvent,
	queue chan<- types.Event,
	req llm.CompletionRequest,
	markerChat, markerTool, markerCode string,
) error {
	ctx, span := telemetry.StartSpan(ctx, "core.StreamCoordinator_Run")
	defer span.End()

	orderedProviders := sc.Router.GetOrderedProviders()
	if len(orderedProviders) == 0 {
		return vraxerror.New(vraxerror.ErrTypeInternal, "no configured model providers available", false, nil)
	}

	var lastErr error
	var streamFinished bool

	for i, entry := range orderedProviders {
		if streamFinished {
			break
		}

		currentReq := req
		currentReq.Model = entry.Config.Model

		if i == 0 {
			if sc.Verbose {
				slog.Info("StreamCoordinator started", "model", entry.Config.Model)
			}
		} else {
			msg := fmt.Sprintf("⚠️  [Fallback] Switching to %s", entry.Config.Model)
			select {
			case out <- llm.StreamEvent{Type: llm.EventTypeStatus, Content: msg}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		stream, err := entry.Provider.StreamGenerate(ctx, currentReq)
		if err != nil {
			if sc.Verbose {
				slog.Warn("StreamCoordinator model stream init failed", "model", entry.Config.Model, "error", err)
			}
			lastErr = err
			continue
		}

		parser := NewStreamParser(markerChat, markerTool, markerCode)
		isCommitted := false

	streamLoop:
		for event := range stream {
			switch event.Type {
			case llm.EventTypeError:
				if !isCommitted {
					if sc.Verbose {
						slog.Warn("StreamCoordinator model stream failed early", "model", entry.Config.Model, "error", event.Err)
					}
					lastErr = event.Err
					break streamLoop
				}
				select {
				case queue <- types.Event{Type: types.EventTypeError, Payload: event.Err}:
				case <-ctx.Done():
				}
				select {
				case out <- event:
				case <-ctx.Done():
				}
				return nil

			case llm.EventTypeToken:
				if chatContent := parser.ProcessToken(event.Content); chatContent != "" {
					isCommitted = true
					
					// SHIELD: Filter out internal control signals from the user-visible stream
					cleanContent := chatContent
					if containsControlSignal(chatContent) {
						cleanContent = ""
					}

					if cleanContent != "" {
						select {
						case queue <- types.Event{Type: types.EventTypeText, Payload: cleanContent}:
						case <-ctx.Done():
						}
						select {
						case out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: cleanContent}:
						case <-ctx.Done():
						}
					}
				}

			case llm.EventTypeDone:
				if tail := parser.Flush(); tail != "" {
					isCommitted = true
					select {
					case queue <- types.Event{Type: types.EventTypeText, Payload: tail}:
					case <-ctx.Done():
					}
					select {
					case out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: tail}:
					case <-ctx.Done():
					}
				}

				toolP := parser.ToolPayload()
				codeP := parser.CodePayload()
				hasToolMarker := parser.HasToolMarker()

				if sc.Verbose {
					slog.Info("Stream Done", "tool_bytes", len(toolP), "code_bytes", len(codeP))
				}

				if len(toolP) > 0 || len(codeP) > 0 || hasToolMarker {
					if len(toolP) == 0 && hasToolMarker {
						// The LLM emitted [VRAX_TOOL] but failed to provide a JSON payload.
						// We inject a special payload to ensure ExecutePipeline bounces an error back.
						toolP = `{"skill_id": "INVALID_EMPTY_PAYLOAD"}`
					}

					select {
					case queue <- types.Event{
						Type: types.EventTypeToolCall,
						Payload: map[string]string{
							"tool_payload": toolP,
							"code_payload": codeP,
						},
					}:
					case <-ctx.Done():
					}
				}

				streamFinished = true
				select {
				case queue <- types.Event{Type: types.EventTypeDone, Payload: nil}:
				case <-ctx.Done():
				}
				break streamLoop
			}
		}
	}

	if !streamFinished {
		err := vraxerror.New(vraxerror.ErrTypeNetwork, "all models failed in cascading fallback loop", false, lastErr)
		select {
		case queue <- types.Event{Type: types.EventTypeError, Payload: err}:
		case <-ctx.Done():
		}
		return err
	}

	return nil
}

func containsControlSignal(s string) bool {
	signals := []string{"◈DELEGATE_BACK", "vraxter-return-control"}
	for _, sig := range signals {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}
