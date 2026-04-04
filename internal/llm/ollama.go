package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatReq struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type ollamaChatRes struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}

// OllamaAdapter implements Provider for local Ollama instances
type OllamaAdapter struct {
	baseURL    string
	httpClient *http.Client
}

func NewOllamaAdapter(host string) *OllamaAdapter {
	if host == "" {
		host = "http://localhost:11434"
	}
	return &OllamaAdapter{
		baseURL:    host + "/api/chat",
		httpClient: &http.Client{},
	}
}

func (a *OllamaAdapter) Generate(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	payload := ollamaChatReq{
		Model:    req.Model,
		Messages: make([]ollamaMessage, len(req.Messages)),
		Stream:   false,
	}

	for i, m := range req.Messages {
		payload.Messages[i] = ollamaMessage{
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

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return CompletionResponse{}, fmt.Errorf("Ollama returned status: %d", resp.StatusCode)
	}

	var parsed ollamaChatRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return CompletionResponse{}, err
	}

	return CompletionResponse{Content: parsed.Message.Content}, nil
}

func (a *OllamaAdapter) StreamGenerate(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
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

func (a *OllamaAdapter) CheckHealth(ctx context.Context) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", a.baseURL[:len(a.baseURL)-9], nil) // Just GET the host
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not running at host: %w", err)
	}
	defer resp.Body.Close()
	return nil
}
