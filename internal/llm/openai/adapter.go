// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/vraxter/vraxter/internal/llm/registry"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/interfaces"
)

func init() {
	registry.Register("openai", func(apiKey, baseURL string) interfaces.LLMProvider {
		return NewAdapter(apiKey, baseURL)
	})
}

// ... (internal types)

type openaiMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

type openaiToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openaiTool struct {
	Type     string             `json:"type"`
	Function openaiToolFunction `json:"function"`
}

type openaiToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type openaiReq struct {
	Model       string          `json:"model"`
	Messages    []openaiMessage `json:"messages"`
	Stream      bool            `json:"stream,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	Seed        *int64          `json:"seed,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Tools       []openaiTool    `json:"tools,omitempty"`
}

type openaiRes struct {
	Choices []struct {
		Message struct {
			Content   string           `json:"content"`
			ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		Delta struct {
			Content   string           `json:"content"`
			ToolCalls []openaiToolCall `json:"tool_calls,omitempty"`
		} `json:"delta"`
	} `json:"choices"`
}

func buildOpenAITools(tools []interfaces.ToolDefinition) []openaiTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]openaiTool, 0, len(tools))
	for _, t := range tools {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		schema := t.ParametersSchema
		if len(bytes.TrimSpace(schema)) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		out = append(out, openaiTool{
			Type: "function",
			Function: openaiToolFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
		})
	}
	return out
}

func parseOpenAIToolCalls(rawCalls []openaiToolCall) []interfaces.ToolCall {
	if len(rawCalls) == 0 {
		return nil
	}
	var out []interfaces.ToolCall
	for _, rc := range rawCalls {
		if strings.TrimSpace(rc.Function.Name) == "" {
			continue
		}
		args := make(map[string]interface{})
		rawArgs := strings.TrimSpace(rc.Function.Arguments)
		if rawArgs != "" {
			_ = json.Unmarshal([]byte(rawArgs), &args)
		}
		out = append(out, interfaces.ToolCall{
			ID:        rc.ID,
			Name:      rc.Function.Name,
			Arguments: args,
			RawJSON:   rawArgs,
		})
	}
	return out
}

type Adapter struct {
	apiKey       string
	baseURL      string
	isCustomHost bool
	httpClient   *http.Client
}

func NewAdapter(apiKey, host string) *Adapter {
	host = strings.TrimSpace(host)
	isCustomHost := host != "" && !strings.Contains(host, "api.openai.com")
	if host == "" {
		host = "https://api.openai.com/v1/chat/completions"
	} else {
		host = strings.TrimRight(host, "/")
		if strings.HasSuffix(host, "/v1") {
			host = host + "/chat/completions"
		} else if !strings.Contains(host, "/chat/completions") {
			host = host + "/v1/chat/completions"
		}
	}

	return &Adapter{
		apiKey:       apiKey,
		baseURL:      host,
		isCustomHost: isCustomHost,
		httpClient:   utils.NewDDILHTTPClient(),
	}
}

func (a *Adapter) endpointURL(path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if strings.HasSuffix(a.baseURL, "/chat/completions") {
		return strings.TrimSuffix(a.baseURL, "/chat/completions") + path
	}
	return strings.TrimRight(a.baseURL, "/") + path
}

func buildOpenAIMessages(msgs []interfaces.Message) []openaiMessage {
	var out []openaiMessage
	for _, m := range msgs {
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		role := m.Role
		if role == "model" {
			role = "assistant"
		}
		if role != "system" && role != "user" && role != "assistant" {
			continue
		}

		if mimeType, b64Data, textPrompt, ok := interfaces.ParseDataURI(m.Content); ok && b64Data != "" {
			if strings.HasPrefix(mimeType, "audio/") {
				format := "wav"
				if strings.Contains(mimeType, "mp3") {
					format = "mp3"
				} else if strings.Contains(mimeType, "ogg") {
					format = "ogg"
				}
				parts := []interface{}{
					map[string]interface{}{
						"type": "input_audio",
						"input_audio": map[string]string{
							"data":   b64Data,
							"format": format,
						},
					},
				}
				if strings.TrimSpace(textPrompt) != "" {
					parts = append(parts, map[string]interface{}{
						"type": "text",
						"text": textPrompt,
					})
				}
				out = append(out, openaiMessage{
					Role:    role,
					Content: parts,
				})
			} else {
				parts := []interface{}{
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]string{
							"url": fmt.Sprintf("data:%s;base64,%s", mimeType, b64Data),
						},
					},
				}
				if strings.TrimSpace(textPrompt) != "" {
					parts = append(parts, map[string]interface{}{
						"type": "text",
						"text": textPrompt,
					})
				}
				out = append(out, openaiMessage{
					Role:    role,
					Content: parts,
				})
			}
		} else {
			out = append(out, openaiMessage{
				Role:    role,
				Content: m.Content,
			})
		}
	}
	return out
}

func (a *Adapter) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
	if !a.isCustomHost && a.apiKey == "" {
		return interfaces.CompletionResponse{}, fmt.Errorf("OpenAI API Key is missing")
	}

	payload := openaiReq{
		Model:       req.Model,
		Messages:    buildOpenAIMessages(req.Messages),
		Stream:      false,
		Temperature: req.Temperature,
		Seed:        req.Seed,
		MaxTokens:   req.MaxTokens,
		TopP:        req.TopP,
		Tools:       buildOpenAITools(req.Tools),
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return interfaces.CompletionResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL, bytes.NewReader(b))
	if err != nil {
		return interfaces.CompletionResponse{}, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return interfaces.CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return interfaces.CompletionResponse{}, fmt.Errorf("OpenAI returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes)))
	}

	var parsed openaiRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return interfaces.CompletionResponse{}, err
	}

	if len(parsed.Choices) == 0 {
		return interfaces.CompletionResponse{}, fmt.Errorf("no choices returned by OpenAI")
	}

	return interfaces.CompletionResponse{
		Content:   parsed.Choices[0].Message.Content,
		ToolCalls: parseOpenAIToolCalls(parsed.Choices[0].Message.ToolCalls),
	}, nil
}

func (a *Adapter) StreamGenerate(ctx context.Context, req interfaces.CompletionRequest) (<-chan interfaces.StreamEvent, error) {
	if !a.isCustomHost && a.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API Key is missing")
	}

	payload := openaiReq{
		Model:       req.Model,
		Messages:    buildOpenAIMessages(req.Messages),
		Stream:      true,
		Temperature: req.Temperature,
		Seed:        req.Seed,
		MaxTokens:   req.MaxTokens,
		TopP:        req.TopP,
		Tools:       buildOpenAITools(req.Tools),
	}

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
	if a.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

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
				Err:  fmt.Errorf("openai stream error %d: %s", resp.StatusCode, strings.TrimSpace(string(bodyBytes))),
			})
			return
		}

		var toolCallOrder []int
		toolCallAccum := make(map[int]*openaiToolCall)
		accumulateCalls := func(calls []openaiToolCall) {
			for idx, tc := range calls {
				slot := tc.Index
				if slot == 0 && idx > 0 {
					slot = idx
				}
				acc, exists := toolCallAccum[slot]
				if !exists {
					cp := tc
					toolCallAccum[slot] = &cp
					toolCallOrder = append(toolCallOrder, slot)
				} else {
					if tc.ID != "" {
						acc.ID = tc.ID
					}
					if tc.Function.Name != "" {
						acc.Function.Name += tc.Function.Name
					}
					if tc.Function.Arguments != "" {
						acc.Function.Arguments += tc.Function.Arguments
					}
				}
			}
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			if ctx.Err() != nil {
				return
			}
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if strings.HasPrefix(line, "data:") {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data == "[DONE]" {
					break
				}
				var chunk openaiRes
				if err := json.Unmarshal([]byte(data), &chunk); err == nil && len(chunk.Choices) > 0 {
					token := chunk.Choices[0].Delta.Content
					if token == "" {
						token = chunk.Choices[0].Message.Content
					}
					if token != "" {
						if !interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeToken, Content: token}) {
							return
						}
					}
					if len(chunk.Choices[0].Delta.ToolCalls) > 0 {
						accumulateCalls(chunk.Choices[0].Delta.ToolCalls)
					}
					if len(chunk.Choices[0].Message.ToolCalls) > 0 {
						accumulateCalls(chunk.Choices[0].Message.ToolCalls)
					}
				}
			} else if strings.HasPrefix(line, "{") {
				// Fallback if endpoint returned a non-SSE single JSON response
				var single openaiRes
				if err := json.Unmarshal([]byte(line), &single); err == nil && len(single.Choices) > 0 {
					token := single.Choices[0].Message.Content
					if token == "" {
						token = single.Choices[0].Delta.Content
					}
					if token != "" {
						if !interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeToken, Content: token}) {
							return
						}
					}
					if len(single.Choices[0].Message.ToolCalls) > 0 {
						accumulateCalls(single.Choices[0].Message.ToolCalls)
					}
					if len(single.Choices[0].Delta.ToolCalls) > 0 {
						accumulateCalls(single.Choices[0].Delta.ToolCalls)
					}
				}
			}
		}

		if err := scanner.Err(); err != nil && ctx.Err() == nil {
			interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: err})
			return
		}

		if len(toolCallOrder) > 0 {
			var orderedRaw []openaiToolCall
			for _, slot := range toolCallOrder {
				if acc := toolCallAccum[slot]; acc != nil {
					orderedRaw = append(orderedRaw, *acc)
				}
			}
			for _, tc := range parseOpenAIToolCalls(orderedRaw) {
				tcCopy := tc
				payloadBytes, _ := json.Marshal(map[string]interface{}{
					"skill_id": tc.Name,
					"params":   tc.Arguments,
				})
				if !interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{
					Type:     interfaces.EventTypeSkillCall,
					SkillID:  tc.Name,
					Content:  string(payloadBytes),
					ToolCall: &tcCopy,
				}) {
					return
				}
			}
		}

		interfaces.SendStreamEvent(ctx, ch, interfaces.StreamEvent{Type: interfaces.EventTypeDone})
	}()
	return ch, nil
}

func (a *Adapter) CheckHealth(ctx context.Context) error {
	if !a.isCustomHost && a.apiKey == "" {
		return fmt.Errorf("not configured")
	}
	healthReq, err := http.NewRequestWithContext(ctx, "GET", a.endpointURL("/models"), nil)
	if err != nil {
		return err
	}
	if a.apiKey != "" {
		healthReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	}
	resp, err := a.httpClient.Do(healthReq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy status: %d", resp.StatusCode)
	}
	return nil
}

func (a *Adapter) SynthesizeSpeech(ctx context.Context, text, voice string) ([]byte, error) {
	if !a.isCustomHost && a.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API Key is missing")
	}

	if voice == "" {
		voice = "alloy"
	}

	payload := map[string]string{
		"model": "tts-1",
		"input": text,
		"voice": voice,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	url := a.endpointURL("/audio/speech")
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI speech synthesis returned status: %d, body: %s", resp.StatusCode, string(respBody))
	}

	return io.ReadAll(resp.Body)
}

func (a *Adapter) TranscribeAudio(ctx context.Context, audioBytes []byte, mimeType, prompt string) (string, error) {
	if !a.isCustomHost && a.apiKey == "" {
		return "", fmt.Errorf("OpenAI API Key is missing")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	ext := "wav"
	if strings.Contains(mimeType, "mp3") {
		ext = "mp3"
	} else if strings.Contains(mimeType, "m4a") {
		ext = "m4a"
	} else if strings.Contains(mimeType, "ogg") {
		ext = "ogg"
	}
	
	part, err := writer.CreateFormFile("file", "audio."+ext)
	if err != nil {
		return "", err
	}
	part.Write(audioBytes)

	writer.WriteField("model", "whisper-1")

	if prompt != "" {
		writer.WriteField("prompt", prompt)
	}
	writer.Close()

	url := a.endpointURL("/audio/transcriptions")
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	if a.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI transcription returned status: %d, body: %s", resp.StatusCode, string(respBody))
	}

	var parsed struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return "", err
	}

	return parsed.Text, nil
}

func (a *Adapter) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if !a.isCustomHost && a.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API Key is missing")
	}

	payload := map[string]interface{}{
		"model": model,
		"input": texts,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	embedURL := a.endpointURL("/embeddings")

	httpReq, err := http.NewRequestWithContext(ctx, "POST", embedURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI embed returned status: %d", resp.StatusCode)
	}

	var parsed struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	var results [][]float32
	for _, raw := range parsed.Data {
		results = append(results, raw.Embedding)
	}
	return results, nil
}

func (a *Adapter) Discover(ctx context.Context) ([]interfaces.ModelMetadata, error) {
	if !a.isCustomHost && a.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API Key is missing")
	}

	reqURL := a.endpointURL("/models")
	httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if a.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI models returned status: %d", resp.StatusCode)
	}

	var parsed struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	var models []interfaces.ModelMetadata
	for _, m := range parsed.Data {
		lowerID := strings.ToLower(m.ID)
		caps := []string{"text"}
		if strings.Contains(lowerID, "embed") {
			caps = []string{"embedding"}
		} else if strings.Contains(lowerID, "dall-e") {
			caps = []string{"image"}
		} else if strings.Contains(lowerID, "tts") || strings.Contains(lowerID, "whisper") {
			caps = []string{"audio"}
		} else if !a.isCustomHost && !strings.HasPrefix(m.ID, "gpt-") && !strings.HasPrefix(m.ID, "o1-") {
			continue
		}

		models = append(models, interfaces.ModelMetadata{
			ID:           m.ID,
			DisplayName:  m.ID,
			Capabilities: caps,
		})
	}
	return models, nil
}

func (a *Adapter) GetModelDetails(ctx context.Context, modelID string) (map[string]interface{}, error) {
	if !a.isCustomHost && a.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API Key is missing")
	}

	reqURL := a.endpointURL("/models/" + modelID)
	httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	if a.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)
	}

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI model info returned status: %d", resp.StatusCode)
	}

	var parsed map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	return parsed, nil
}
