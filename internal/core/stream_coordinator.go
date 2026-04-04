package core

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/patagonicrune/vraxter/internal/llm"
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
			out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n" + msg + "\n"}
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
				queue <- types.Event{Type: types.EventTypeError, Payload: event.Err}
				out <- event
				return nil

			case llm.EventTypeToken:
				if chatContent := parser.ProcessToken(event.Content); chatContent != "" {
					isCommitted = true
					queue <- types.Event{Type: types.EventTypeText, Payload: chatContent}
					out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: chatContent}
				}

			case llm.EventTypeDone:
				if tail := parser.Flush(); tail != "" {
					isCommitted = true
					queue <- types.Event{Type: types.EventTypeText, Payload: tail}
					out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: tail}
				}

				toolP := parser.ToolPayload()
				codeP := parser.CodePayload()

				if sc.Verbose {
					slog.Info("Stream Done", "tool_bytes", len(toolP), "code_bytes", len(codeP))
				}

				if len(toolP) > 0 {
					queue <- types.Event{
						Type: types.EventTypeToolCall,
						Payload: map[string]string{
							"tool_payload": toolP,
							"code_payload": codeP,
						},
					}
				}

				streamFinished = true
				queue <- types.Event{Type: types.EventTypeDone, Payload: nil}
				out <- event
				break streamLoop
			}
		}
	}

	if !streamFinished {
		err := vraxerror.New(vraxerror.ErrTypeNetwork, "all models failed in cascading fallback loop", false, lastErr)
		queue <- types.Event{Type: types.EventTypeError, Payload: err}
		return err
	}

	return nil
}
