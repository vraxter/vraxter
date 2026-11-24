// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/vraxter/vraxter/internal/llm/registry"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/interfaces"
)

func init() {
	registry.Register("anthropic", func(apiKey, baseURL string) interfaces.LLMProvider {
		return NewAdapter(apiKey, baseURL)
	})
}

type anthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthropicReq struct {
	Model       string             `json:"model"`
	MaxTokens   int                `json:"max_tokens"`
	System      string             `json:"system,omitempty"`
	Messages    []anthropicMessage `json:"messages"`
	Temperature *float64           `json:"temperature,omitempty"`
	TopP        *float64           `json:"top_p,omitempty"`
	Stream      bool               `json:"stream,omitempty"`
	Tools       []anthropicTool    `json:"tools,omitempty"`
}

type anthropicContentBlock struct {
	Type  string                 `json:"type"`
	Text  string                 `json:"text,omitempty"`
	ID    string                 `json:"id,omitempty"`
	Name  string                 `json:"name,omitempty"`
	Input map[string]interface{} `json:"input,omitempty"`
}

type anthropicRes struct {
	Content []anthropicContentBlock `json:"content"`
}

type anthropicSSEEnvelope struct {
	Type         string                 `json:"type"`
	Index        int                    `json:"index,omitempty"`
	ContentBlock *anthropicContentBlock `json:"content_block,omitempty"`
	Delta        *struct {
		Type        string `json:"type"`
		Text        string `json:"text,omitempty"`
		PartialJSON string `json:"partial_json,omitempty"`
	} `json:"delta,omitempty"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type Adapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewAdapter(apiKey, host string) *Adapter {
	if host == "" {
		host = "https://api.anthropic.com/v1/messages"
	}
	return &Adapter{
		apiKey:     apiKey,
		baseURL:    host,
		httpClient: utils.NewDDILHTTPClient(),
	}
}

func buildAnthropicPayload(req interfaces.CompletionRequest, stream bool) anthropicReq {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	payload := anthropicReq{
		Model:       req.Model,
		MaxTokens:   maxTokens,
		Messages:    make([]anthropicMessage, 0),
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stream:      stream,
	}

	if len(req.Tools) > 0 {
		for _, t := range req.Tools {
			if strings.TrimSpace(t.Name) == "" {
				continue
			}
			schema := t.ParametersSchema
			if len(bytes.TrimSpace(schema)) == 0 {
				schema = json.RawMessage(`{"type":"object","properties":{}}`)
			}
			payload.Tools = append(payload.Tools, anthropicTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: schema,
			})
		}
	}

	for _, m := range req.Messages {
		if m.Role == "system" {
			if payload.System == "" {
				payload.System = m.Content
			} else {
				payload.System += "\n\n" + m.Content
			}
		} else {
			if mimeType, b64Data, textPrompt, ok := interfaces.ParseDataURI(m.Content); ok {
				parts := []interface{}{
					map[string]interface{}{
						"type": "image",
						"source": map[string]string{
							"type":       "base64",
							"media_type": mimeType,
							"data":       b64Data,
						},
					},
				}
				if textPrompt != "" {
					parts = append(parts, map[string]interface{}{
						"type": "text",
						"text": textPrompt,
					})
				}
				payload.Messages = append(payload.Messages, anthropicMessage{
					Role:    m.Role,
					Content: parts,
				})
			} else {
				payload.Messages = append(payload.Messages, anthropicMessage{
					Role:    m.Role,
					Content: m.Content,
				})
			}
		}
	}
	return payload
}

func (a *Adapter) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
	if a.apiKey == "" {
		return interfaces.CompletionResponse{}, fmt.Errorf("Anthropic API Key is missing")
	}

	payload := buildAnthropicPayload(req, false)
	b, err := json.Marshal(payload)
	if err != nil {
		return interfaces.CompletionResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL, bytes.NewReader(b))
	if err != nil {
		return interfaces.CompletionResponse{}, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return interfaces.CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return interfaces.CompletionResponse{}, fmt.Errorf("Anthropic returned status %s: %s", resp.Status, strings.TrimSpace(string(bodyBytes)))
	}

	var parsed anthropicRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return interfaces.CompletionResponse{}, err
	}

	if len(parsed.Content) == 0 {
		return interfaces.CompletionResponse{}, fmt.Errorf("no content returned by Anthropic")
	}

	var textBuilder strings.Builder
	var toolCalls []interfaces.ToolCall
	for _, block := range parsed.Content {
		if block.Type == "tool_use" && strings.TrimSpace(block.Name) != "" {
			rawBytes, _ := json.Marshal(block.Input)
			toolCalls = append(toolCalls, interfaces.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: block.Input,
				RawJSON:   string(rawBytes),
			})
		} else if block.Text != "" {
			textBuilder.WriteString(block.Text)
		}
	}

	return interfaces.CompletionResponse{
		Content:   textBuilder.String(),
		ToolCalls: toolCalls,
	}, nil
}

// StreamGenerate executes a real Server-Sent Events (SSE) streaming request against Anthropic's
// /v1/messages endpoint, parsing content_block_delta (text_delta, input_json_delta),
// content_block_start/stop, message_stop, and error events incrementally with context cancellation.
func (a *Adapter) StreamGenerate(ctx context.Context, req interfaces.CompletionRequest) (<-chan interfaces.StreamEvent, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("Anthropic API Key is missing")
	}

	payload := buildAnthropicPayload(req, true)
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	ch := make(chan interfaces.StreamEvent)
	go func() {
		defer close(ch)
		resp, err := a.httpClient.Do(httpReq)
		if err != nil {
			interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: err})
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{
				Type: interfaces.EventTypeError,
				Err:  fmt.Errorf("anthropic stream error %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes))),
			})
			return
		}

		type pendingToolUse struct {
			id      string
			name    string
			initial map[string]interface{}
			jsonBuf strings.Builder
			emitted bool
		}
		activeTools := make(map[int]*pendingToolUse)
		var toolOrder []int

		flushTool := func(pt *pendingToolUse) bool {
			if pt == nil || pt.emitted || strings.TrimSpace(pt.name) == "" {
				return true
			}
			pt.emitted = true
			args := make(map[string]interface{})
			rawJSON := strings.TrimSpace(pt.jsonBuf.String())
			if rawJSON != "" {
				_ = json.Unmarshal([]byte(rawJSON), &args)
			} else if len(pt.initial) > 0 {
				args = pt.initial
				b, _ := json.Marshal(args)
				rawJSON = string(b)
			}
			tc := interfaces.ToolCall{
				ID:        pt.id,
				Name:      pt.name,
				Arguments: args,
				RawJSON:   rawJSON,
			}
			payloadBytes, _ := json.Marshal(map[string]interface{}{
				"skill_id": tc.Name,
				"params":   tc.Arguments,
			})
			return interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{
				Type:     interfaces.EventTypeSkillCall,
				SkillID:  tc.Name,
				Content:  string(payloadBytes),
				ToolCall: &tc,
			})
		}

		var currentEvent string
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "" {
				currentEvent = ""
				continue
			}
			if strings.HasPrefix(line, ":") {
				continue
			}
			if strings.HasPrefix(line, "event:") {
				currentEvent = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data == "" || data == "[DONE]" {
					continue
				}

				var env anthropicSSEEnvelope
				if err := json.Unmarshal([]byte(data), &env); err != nil {
					continue
				}
				evType := env.Type
				if evType == "" {
					evType = currentEvent
				}

				switch evType {
				case "error":
					msg := "anthropic stream error"
					if env.Error != nil && env.Error.Message != "" {
						msg = fmt.Sprintf("anthropic stream error (%s): %s", env.Error.Type, env.Error.Message)
					}
					interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{
						Type: interfaces.EventTypeError,
						Err:  fmt.Errorf("%s", msg),
					})
					return

				case "content_block_start":
					if env.ContentBlock != nil {
						if env.ContentBlock.Type == "tool_use" && strings.TrimSpace(env.ContentBlock.Name) != "" {
							pt := &pendingToolUse{
								id:      env.ContentBlock.ID,
								name:    env.ContentBlock.Name,
								initial: env.ContentBlock.Input,
							}
							activeTools[env.Index] = pt
							toolOrder = append(toolOrder, env.Index)
						} else if env.ContentBlock.Text != "" {
							if !interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{
								Type:    interfaces.EventTypeToken,
								Content: env.ContentBlock.Text,
							}) {
								return
							}
						}
					}

				case "content_block_delta":
					if env.Delta != nil {
						if env.Delta.Type == "text_delta" || (env.Delta.Type == "" && env.Delta.Text != "") {
							if env.Delta.Text != "" {
								if !interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{
									Type:    interfaces.EventTypeToken,
									Content: env.Delta.Text,
								}) {
									return
								}
							}
						} else if env.Delta.Type == "input_json_delta" || env.Delta.PartialJSON != "" {
							pt, ok := activeTools[env.Index]
							if !ok {
								pt = &pendingToolUse{}
								activeTools[env.Index] = pt
								toolOrder = append(toolOrder, env.Index)
							}
							pt.jsonBuf.WriteString(env.Delta.PartialJSON)
						}
					}

				case "content_block_stop":
					if pt, ok := activeTools[env.Index]; ok {
						if !flushTool(pt) {
							return
						}
					}

				case "message_stop":
					for _, idx := range toolOrder {
						if !flushTool(activeTools[idx]) {
							return
						}
					}
					interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeDone})
					return
				}
			} else if strings.HasPrefix(line, "{") {
				// Fallback if server returned a single non-SSE JSON response body
				var single anthropicRes
				if err := json.Unmarshal([]byte(line), &single); err == nil && len(single.Content) > 0 {
					for _, block := range single.Content {
						if block.Text != "" {
							if !interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{
								Type:    interfaces.EventTypeToken,
								Content: block.Text,
							}) {
								return
							}
						}
						if block.Type == "tool_use" && strings.TrimSpace(block.Name) != "" {
							rawBytes, _ := json.Marshal(block.Input)
							tc := interfaces.ToolCall{
								ID:        block.ID,
								Name:      block.Name,
								Arguments: block.Input,
								RawJSON:   string(rawBytes),
							}
							payloadBytes, _ := json.Marshal(map[string]interface{}{
								"skill_id": tc.Name,
								"params":   tc.Arguments,
							})
							if !interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{
								Type:     interfaces.EventTypeSkillCall,
								SkillID:  tc.Name,
								Content:  string(payloadBytes),
								ToolCall: &tc,
							}) {
								return
							}
						}
					}
				}
			}
		}

		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: err})
			return
		}

		for _, idx := range toolOrder {
			if !flushTool(activeTools[idx]) {
				return
			}
		}
		interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeDone})
	}()
	return ch, nil
}

func (a *Adapter) CheckHealth(ctx context.Context) error {
	if a.apiKey == "" {
		return fmt.Errorf("not configured")
	}
	return nil
}

func (a *Adapter) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	return nil, fmt.Errorf("unimplemented: anthropic embeddings")
}

func (a *Adapter) Discover(ctx context.Context) ([]interfaces.ModelMetadata, error) {
	raw := []string{
		"claude-3-5-sonnet-20240620",
		"claude-3-opus-20240229",
		"claude-3-sonnet-20240229",
		"claude-3-haiku-20240307",
	}
	var res []interfaces.ModelMetadata
	for _, m := range raw {
		res = append(res, interfaces.ModelMetadata{
			ID:           m,
			DisplayName:  m,
			Capabilities: []string{"text", "vision", "tools"},
		})
	}
	return res, nil
}

func (a *Adapter) GetModelDetails(ctx context.Context, modelID string) (map[string]interface{}, error) {
	return map[string]interface{}{"id": modelID, "provider": "anthropic"}, nil
}
