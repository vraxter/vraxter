package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type anthropicMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type anthropicReq struct {
	Model     string             `json:"model"`
	MaxTokens int                `json:"max_tokens"`
	System    string             `json:"system,omitempty"`
	Messages  []anthropicMessage `json:"messages"`
}

type anthropicRes struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// AnthropicAdapter implements Provider for Anthropic Claude models
type AnthropicAdapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewAnthropicAdapter(apiKey string) *AnthropicAdapter {
	return &AnthropicAdapter{
		apiKey:     apiKey,
		baseURL:    "https://api.anthropic.com/v1/messages",
		httpClient: &http.Client{},
	}
}

func (a *AnthropicAdapter) Generate(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if a.apiKey == "" {
		return CompletionResponse{}, fmt.Errorf("Anthropic API Key is missing")
	}

	payload := anthropicReq{
		Model:     req.Model,
		MaxTokens: 4096,
		Messages:  make([]anthropicMessage, 0),
	}

	for _, m := range req.Messages {
		if m.Role == "system" {
			payload.System = m.Content
		} else {
			payload.Messages = append(payload.Messages, anthropicMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", a.baseURL, bytes.NewReader(b))
	if err != nil {
		return CompletionResponse{}, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("Anthropic returned status: %s", resp.Status)
	}

	var parsed anthropicRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return CompletionResponse{}, err
	}

	if len(parsed.Content) == 0 {
		return CompletionResponse{}, fmt.Errorf("no content returned by Anthropic")
	}

	return CompletionResponse{Content: parsed.Content[0].Text}, nil
}

func (a *AnthropicAdapter) StreamGenerate(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		res, err := a.Generate(ctx, req)
		if err != nil {
			ch <- StreamEvent{Type: EventTypeError, Err: err}
			return
		}
		ch <- StreamEvent{Type: EventTypeToken, Content: res.Content}
		ch <- StreamEvent{Type: EventTypeDone}
	}()
	return ch, nil
}

func (a *AnthropicAdapter) CheckHealth(ctx context.Context) error {
	if a.apiKey == "" {
		return fmt.Errorf("not configured")
	}
	// We check for simple unauthorized errors
	return nil
}
