package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type geminiPart struct {
	Text string `json:"text"`
}

type geminiContent struct {
	Role  string       `json:"role"`
	Parts []geminiPart `json:"parts"`
}

type geminiReq struct {
	Contents          []geminiContent `json:"contents"`
	SystemInstruction *geminiContent  `json:"system_instruction,omitempty"`
}

type geminiRes struct {
	Candidates []struct {
		Content struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
}

type geminiModelsRes struct {
	Models []struct {
		Name string `json:"name"`
	} `json:"models"`
}

// GeminiAdapter implements Provider for Google Gemini API REST
type GeminiAdapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewGeminiAdapter(apiKey string) *GeminiAdapter {
	return &GeminiAdapter{
		apiKey:     apiKey,
		baseURL:    "https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s",
		httpClient: &http.Client{},
	}
}

func (a *GeminiAdapter) Generate(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	if a.apiKey == "" {
		return CompletionResponse{}, fmt.Errorf("Gemini API Key is missing")
	}

	var systemMsg *geminiContent
	var contents []geminiContent

	for _, m := range req.Messages {
		role := m.Role
		if role == "system" {
			// Pull it out to the specialized field
			systemMsg = &geminiContent{
				Parts: []geminiPart{{Text: m.Content}},
			}
			continue
		}

		if role == "assistant" {
			role = "model"
		}

		contents = append(contents, geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: m.Content}},
		})
	}

	payload := geminiReq{
		Contents:          contents,
		SystemInstruction: systemMsg,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return CompletionResponse{}, err
	}

	url := fmt.Sprintf(a.baseURL, req.Model, a.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
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
		// Log error body for debugging
		var errData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errData)
		return CompletionResponse{}, fmt.Errorf("Gemini (v1) error %d: %v", resp.StatusCode, errData)
	}

	var parsed geminiRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return CompletionResponse{}, err
	}

	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return CompletionResponse{}, fmt.Errorf("no content returned by Gemini")
	}

	return CompletionResponse{Content: parsed.Candidates[0].Content.Parts[0].Text}, nil
}

func (a *GeminiAdapter) StreamGenerate(ctx context.Context, req CompletionRequest) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent)

	// Build Payload
	var systemMsg *geminiContent
	var contents []geminiContent

	modelName := req.Model
	if !strings.HasPrefix(modelName, "models/") {
		modelName = "models/" + modelName
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "system_instruction":
			systemMsg = &geminiContent{Parts: []geminiPart{{Text: m.Content}}}
		case "user":
			contents = append(contents, geminiContent{
				Role:  "user",
				Parts: []geminiPart{{Text: m.Content}},
			})
		case "assistant", "model":
			contents = append(contents, geminiContent{
				Role:  "model",
				Parts: []geminiPart{{Text: m.Content}},
			})
		default:
			// Ignorar roles desconocidos para que no rompan el JSON
			continue
		}
	}

	payload := geminiReq{
		Contents:          contents,
		SystemInstruction: systemMsg,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	// Use streamGenerateContent endpoint
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/%s:streamGenerateContent?key=%s", modelName, a.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	go func() {
		defer close(ch)
		resp, err := a.httpClient.Do(httpReq)
		if err != nil {
			ch <- StreamEvent{Type: EventTypeError, Err: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)

			// 2. Intentar parsear el JSON del error
			var errData []map[string]interface{}
			json.Unmarshal(bodyBytes, &errData)

			ch <- StreamEvent{Type: EventTypeError, Err: fmt.Errorf("gemini stream error %d: %v", resp.StatusCode, errData)}
			return
		}

		// Google's Stream response is a JSON array of response objects
		// [ { "candidates": [...] }, { "candidates": [...] } ]
		// We can use a custom decoder to handle the comma-separated objects in the array
		dec := json.NewDecoder(resp.Body)

		// Read the opening bracket '['
		_, err = dec.Token()
		if err != nil {
			ch <- StreamEvent{Type: EventTypeError, Err: fmt.Errorf("failed to parse stream start: %w", err)}
			return
		}

		for dec.More() {
			var chunk geminiRes
			if err := dec.Decode(&chunk); err != nil {
				ch <- StreamEvent{Type: EventTypeError, Err: fmt.Errorf("failed to decode chunk: %w", err)}
				return
			}

			if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 {
				token := chunk.Candidates[0].Content.Parts[0].Text
				ch <- StreamEvent{Type: EventTypeToken, Content: token}
			}
		}

		ch <- StreamEvent{Type: EventTypeDone}
	}()

	return ch, nil
}

func (a *GeminiAdapter) CheckHealth(ctx context.Context) error {
	if a.apiKey == "" {
		return fmt.Errorf("not configured")
	}

	// Real HealthCheck: list available models to verify Auth key
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1/models?key=%s", a.apiKey)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to reach google ai studio: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("google AI Studio rejected key: status %d", resp.StatusCode)
	}

	var models geminiModelsRes
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return fmt.Errorf("failed to parse models list: %w", err)
	}

	if len(models.Models) == 0 {
		return fmt.Errorf("api key is valid but has no accessible models")
	}

	return nil
}
