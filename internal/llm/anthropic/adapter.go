package anthropic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/patagonicrune/vraxter/internal/llm/registry"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
)

func init() {
	registry.Register("anthropic", func(apiKey, baseURL string) interfaces.LLMProvider {
		return NewAdapter(apiKey, baseURL)
	})
}

// ... (internal types)

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
		httpClient: &http.Client{},
	}
}

func (a *Adapter) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
	if a.apiKey == "" {
		return interfaces.CompletionResponse{}, fmt.Errorf("Anthropic API Key is missing")
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
		return interfaces.CompletionResponse{}, fmt.Errorf("Anthropic returned status: %s", resp.Status)
	}

	var parsed anthropicRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return interfaces.CompletionResponse{}, err
	}

	if len(parsed.Content) == 0 {
		return interfaces.CompletionResponse{}, fmt.Errorf("no content returned by Anthropic")
	}

	return interfaces.CompletionResponse{Content: parsed.Content[0].Text}, nil
}

func (a *Adapter) StreamGenerate(ctx context.Context, req interfaces.CompletionRequest) (<-chan interfaces.StreamEvent, error) {
	ch := make(chan interfaces.StreamEvent)
	go func() {
		defer close(ch)
		res, err := a.Generate(ctx, req)
		if err != nil {
			ch <- interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: err}
			return
		}
		ch <- interfaces.StreamEvent{Type: interfaces.EventTypeToken, Content: res.Content}
		ch <- interfaces.StreamEvent{Type: interfaces.EventTypeDone}
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
