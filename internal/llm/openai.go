package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type openaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openaiReq struct {
	Model    string          `json:"model"`
	Messages []openaiMessage `json:"messages"`
}

type openaiRes struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// OpenAIAdapter implements Provider for OpenAI (and Grok/xAI compatible) API
type OpenAIAdapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewOpenAIAdapter(apiKey string) *OpenAIAdapter {
	return &OpenAIAdapter{
		apiKey:     apiKey,
		baseURL:    "https://api.openai.com/v1/chat/completions",
		httpClient: &http.Client{},
	}
}

func (a *OpenAIAdapter) Generate(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if a.apiKey == "" {
		return CompletionResponse{}, fmt.Errorf("OpenAI API Key is missing")
	}

	payload := openaiReq{
		Model:    req.Model,
		Messages: make([]openaiMessage, len(req.Messages)),
	}

	for i, m := range req.Messages {
		payload.Messages[i] = openaiMessage{
			Role:    m.Role,
			Content: m.Content,
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
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("OpenAI returned status: %s", resp.Status)
	}

	var parsed openaiRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return CompletionResponse{}, err
	}

	if len(parsed.Choices) == 0 {
		return CompletionResponse{}, fmt.Errorf("no choices returned by OpenAI")
	}

	return CompletionResponse{Content: parsed.Choices[0].Message.Content}, nil
}

func (a *OpenAIAdapter) StreamGenerate(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
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

func (a *OpenAIAdapter) CheckHealth(ctx context.Context) error {
	if a.apiKey == "" {
		return fmt.Errorf("not configured")
	}
	// Models endpoint is a good lightweight check
	healthReq, _ := http.NewRequestWithContext(ctx, "GET", "https://api.openai.com/v1/models", nil)
	healthReq.Header.Set("Authorization", "Bearer "+a.apiKey)
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

func (a *OpenAIAdapter) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if a.apiKey == "" {
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

	// We append /embeddings to the base URL domain instead of /chat/completions
	embedURL := strings.Replace(a.baseURL, "chat/completions", "embeddings", 1)

	httpReq, err := http.NewRequestWithContext(ctx, "POST", embedURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

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
