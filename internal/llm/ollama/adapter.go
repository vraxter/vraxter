package ollama

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

	"github.com/patagonicrune/vraxter/internal/llm/registry"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
)

func init() {
	registry.Register("ollama", func(apiKey, baseURL string) interfaces.LLMProvider {
		return NewAdapter(baseURL)
	})
}

type ollamaMessage struct {
	Role    string   `json:"role"`
	Content string   `json:"content"`
	Images  []string `json:"images,omitempty"`
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

type Adapter struct {
	baseURL    string
	httpClient *http.Client
}

func NewAdapter(host string) *Adapter {
	if host == "" {
		host = "http://localhost:11434"
	}
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	return &Adapter{
		baseURL: host + "/api/chat",
		httpClient: &http.Client{
			Timeout: 20 * time.Minute,
			Transport: &http.Transport{
				ResponseHeaderTimeout: 60 * time.Second,
			},
		},
	}
}

func (a *Adapter) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
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
		if mimeType, b64Data, textPrompt, ok := interfaces.ParseDataURI(m.Content); ok {
			if strings.HasPrefix(mimeType, "image/") {
				payload.Messages[i] = ollamaMessage{
					Role:    m.Role,
					Content: textPrompt,
					Images:  []string{b64Data},
				}
			} else {
				payload.Messages[i] = ollamaMessage{
					Role:    m.Role,
					Content: fmt.Sprintf("[Audio input block of type %s] %s", mimeType, textPrompt),
				}
			}
		} else {
			payload.Messages[i] = ollamaMessage{
				Role:    m.Role,
				Content: m.Content,
			}
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

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return interfaces.CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return interfaces.CompletionResponse{}, fmt.Errorf("Ollama returned status: %d", resp.StatusCode)
	}

	var parsed ollamaChatRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return interfaces.CompletionResponse{}, err
	}

	return interfaces.CompletionResponse{Content: parsed.Message.Content}, nil
}

func (a *Adapter) StreamGenerate(ctx context.Context, req interfaces.CompletionRequest) (<-chan interfaces.StreamEvent, error) {
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
		if mimeType, b64Data, textPrompt, ok := interfaces.ParseDataURI(m.Content); ok {
			if strings.HasPrefix(mimeType, "image/") {
				payload.Messages[i] = ollamaMessage{
					Role:    m.Role,
					Content: textPrompt,
					Images:  []string{b64Data},
				}
			} else {
				payload.Messages[i] = ollamaMessage{
					Role:    m.Role,
					Content: fmt.Sprintf("[Audio input block of type %s] %s", mimeType, textPrompt),
				}
			}
		} else {
			payload.Messages[i] = ollamaMessage{
				Role:    m.Role,
				Content: m.Content,
			}
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

	ch := make(chan interfaces.StreamEvent)
	go func() {
		defer resp.Body.Close()
		defer close(ch)

		if resp.StatusCode != http.StatusOK {
			ch <- interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: fmt.Errorf("Ollama returned status: %d", resp.StatusCode)}
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
				Eval bool   `json:"eval"`
				Error string `json:"error"`
			}
			if err := decoder.Decode(&chunk); err != nil {
				if err == io.EOF {
					ch <- interfaces.StreamEvent{Type: interfaces.EventTypeDone}
					return
				}
				ch <- interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: fmt.Errorf("ollama stream decode error: %w", err)}
				return
			}
			if chunk.Error != "" {
				ch <- interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: fmt.Errorf("ollama reported error: %s", chunk.Error)}
				return
			}
			if chunk.Message.Content != "" {
				if firstToken {
					slog.Info("Ollama first token arrived", "content", chunk.Message.Content)
					firstToken = false
				}
				ch <- interfaces.StreamEvent{Type: interfaces.EventTypeToken, Content: chunk.Message.Content}
			}
			if chunk.Done {
				ch <- interfaces.StreamEvent{Type: interfaces.EventTypeDone}
				return
			}
		}
	}()

	return ch, nil
}

func (a *Adapter) CheckHealth(ctx context.Context) error {
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

func (a *Adapter) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
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

func (a *Adapter) Discover(ctx context.Context) ([]interfaces.ModelMetadata, error) {
	host := a.baseURL[:len(a.baseURL)-9]
	tagsURL := host + "/api/tags"

	req, err := http.NewRequestWithContext(ctx, "GET", tagsURL, nil)
	if err != nil {
		return nil, err
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Ollama tags returned status: %d", resp.StatusCode)
	}

	var parsed struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	var models []interfaces.ModelMetadata
	for _, m := range parsed.Models {
		caps := []string{"text"}
		if strings.Contains(m.Name, "embed") {
			caps = []string{"embedding"}
		}
		models = append(models, interfaces.ModelMetadata{
			ID:           m.Name,
			DisplayName:  m.Name,
			Capabilities: caps,
		})
	}
	return models, nil
}

func (a *Adapter) GetModelDetails(ctx context.Context, modelID string) (map[string]interface{}, error) {
	host := a.baseURL[:len(a.baseURL)-9]
	showURL := host + "/api/show"

	payload := map[string]string{"name": modelID}
	b, _ := json.Marshal(payload)

	req, _ := http.NewRequestWithContext(ctx, "POST", showURL, bytes.NewReader(b))
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var parsed map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&parsed)
	return parsed, nil
}
