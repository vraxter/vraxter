package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
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
	// Ensure protocol scheme
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	return &OllamaAdapter{
		baseURL: host + "/api/chat",
		httpClient: &http.Client{
			Timeout: 20 * time.Minute,
			Transport: &http.Transport{
				ResponseHeaderTimeout: 60 * time.Second, // More time for large model load
			},
		},
	}
}

func (a *OllamaAdapter) Generate(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	model := req.Model
	if !strings.Contains(model, ":") {
		model += ":latest"
	}

	payload := ollamaChatReq{
		Model:    model,
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
	model := req.Model
	if !strings.Contains(model, ":") {
		model += ":latest"
	}

	payload := ollamaChatReq{
		Model:    model,
		Messages: make([]ollamaMessage, len(req.Messages)),
		Stream:   true,
	}

	for i, m := range req.Messages {
		payload.Messages[i] = ollamaMessage{
			Role:    m.Role,
			Content: m.Content,
		}
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

	slog.Info("Ollama streaming request started", "model", req.Model, "url", a.baseURL)
	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamEvent)
	go func() {
		defer resp.Body.Close()
		defer close(ch)

		if resp.StatusCode != http.StatusOK {
			ch <- StreamEvent{Type: EventTypeError, Err: fmt.Errorf("Ollama returned status: %d", resp.StatusCode)}
			return
		}

		firstToken := true
		decoder := json.NewDecoder(resp.Body)
		for {
			var chunk struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				Done bool   `json:"done"`
				Eval bool   `json:"eval"` // Some ollama versions use this
				Error string `json:"error"`
			}
			if err := decoder.Decode(&chunk); err != nil {
				if err == io.EOF {
					ch <- StreamEvent{Type: EventTypeDone}
					return
				}
				ch <- StreamEvent{Type: EventTypeError, Err: fmt.Errorf("ollama stream decode error: %w", err)}
				return
			}
			if chunk.Error != "" {
				ch <- StreamEvent{Type: EventTypeError, Err: fmt.Errorf("ollama reported error: %s", chunk.Error)}
				return
			}
			if chunk.Message.Content != "" {
				if firstToken {
					slog.Info("Ollama first token arrived", "content", chunk.Message.Content)
					firstToken = false
				}
				ch <- StreamEvent{Type: EventTypeToken, Content: chunk.Message.Content}
			}
			if chunk.Done {
				ch <- StreamEvent{Type: EventTypeDone}
				return
			}
		}
	}()

	return ch, nil
}

func (a *OllamaAdapter) CheckHealth(ctx context.Context) error {
	host := a.baseURL[:len(a.baseURL)-9]
	req, err := http.NewRequestWithContext(ctx, "GET", host, nil)
	if err != nil {
		return fmt.Errorf("invalid ollama host URL: %w", err)
	}
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("ollama not running at host: %w", err)
	}
	defer resp.Body.Close()
	return nil
}

func (a *OllamaAdapter) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	// Ollama /api/embed takes {"model": "...", "input": ["..."]}
	host := a.baseURL[:len(a.baseURL)-9]
	embedURL := host + "/api/embed"
	
	payload := map[string]interface{}{
		"model": model,
		"input": texts,
	}
	
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	
	httpReq, err := http.NewRequestWithContext(ctx, "POST", embedURL, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	
	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama embed returned status: %d", resp.StatusCode)
	}
	
	var parsed struct {
		Embeddings [][]float32 `json:"embeddings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	
	return parsed.Embeddings, nil
}
