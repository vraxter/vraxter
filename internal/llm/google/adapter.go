package google

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/patagonicrune/vraxter/internal/llm/registry"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
)

func init() {
	registry.Register("google", func(apiKey, baseURL string) interfaces.LLMProvider {
		return NewAdapter(apiKey, baseURL)
	})
}

// ... (internal types)

type inlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiPart struct {
	Text       string      `json:"text,omitempty"`
	InlineData *inlineData `json:"inline_data,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
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
		Name             string `json:"name"`
		DisplayName      string `json:"displayName"`
		Description      string `json:"description"`
		InputTokenLimit  int    `json:"inputTokenLimit"`
		OutputTokenLimit int    `json:"outputTokenLimit"`
	} `json:"models"`
}

type Adapter struct {
	apiKey     string
	host       string // e.g. "https://generativelanguage.googleapis.com"
	httpClient *http.Client
}

func NewAdapter(apiKey, host string) *Adapter {
	if host == "" {
		host = "https://generativelanguage.googleapis.com"
	}
	return &Adapter{
		apiKey:     apiKey,
		host:       strings.TrimSuffix(host, "/"),
		httpClient: &http.Client{},
	}
}

func (a *Adapter) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
	if a.apiKey == "" {
		return interfaces.CompletionResponse{}, fmt.Errorf("Google API Key is missing")
	}

	var systemMsg *geminiContent
	var contents []geminiContent

	for _, m := range req.Messages {
		role := m.Role
		if role == "system" {
			if systemMsg == nil {
				systemMsg = &geminiContent{
					Parts: []geminiPart{{Text: m.Content}},
				}
			} else {
				systemMsg.Parts[0].Text += "\n\n" + m.Content
			}
			continue
		}

		if role == "assistant" {
			role = "model"
		}

		if mimeType, b64Data, textPrompt, ok := interfaces.ParseDataURI(m.Content); ok {
			parts := []geminiPart{
				{
					InlineData: &inlineData{
						MimeType: mimeType,
						Data:     b64Data,
					},
				},
			}
			if textPrompt != "" {
				parts = append(parts, geminiPart{Text: textPrompt})
			}
			contents = append(contents, geminiContent{
				Role:  role,
				Parts: parts,
			})
		} else {
			contents = append(contents, geminiContent{
				Role:  role,
				Parts: []geminiPart{{Text: m.Content}},
			})
		}
	}

	payload := geminiReq{
		Contents:          contents,
		SystemInstruction: systemMsg,
	}

	b, err := json.Marshal(payload)
	if err != nil {
		return interfaces.CompletionResponse{}, err
	}

	modelName := req.Model
	if !strings.HasPrefix(modelName, "models/") {
		modelName = "models/" + modelName
	}
	url := fmt.Sprintf("%s/v1beta/%s:generateContent?key=%s", a.host, modelName, a.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
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
		var errData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errData)
		return interfaces.CompletionResponse{}, fmt.Errorf("Google AI (v1) error %d: %v", resp.StatusCode, errData)
	}

	var parsed geminiRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return interfaces.CompletionResponse{}, err
	}

	if len(parsed.Candidates) == 0 || len(parsed.Candidates[0].Content.Parts) == 0 {
		return interfaces.CompletionResponse{}, fmt.Errorf("no content returned by Google")
	}

	return interfaces.CompletionResponse{Content: parsed.Candidates[0].Content.Parts[0].Text}, nil
}

func (a *Adapter) StreamGenerate(ctx context.Context, req interfaces.CompletionRequest) (<-chan interfaces.StreamEvent, error) {
	ch := make(chan interfaces.StreamEvent)

	var systemMsg *geminiContent
	var contents []geminiContent

	modelName := req.Model
	if !strings.HasPrefix(modelName, "models/") {
		modelName = "models/" + modelName
	}

	for _, m := range req.Messages {
		switch m.Role {
		case "system":
			if systemMsg == nil {
				systemMsg = &geminiContent{Parts: []geminiPart{{Text: m.Content}}}
			} else {
				systemMsg.Parts[0].Text += "\n\n" + m.Content
			}
		case "user", "assistant", "model":
			role := "user"
			if m.Role == "assistant" || m.Role == "model" {
				role = "model"
			}
			
			if mimeType, b64Data, textPrompt, ok := interfaces.ParseDataURI(m.Content); ok {
				parts := []geminiPart{
					{
						InlineData: &inlineData{
							MimeType: mimeType,
							Data:     b64Data,
						},
					},
				}
				if textPrompt != "" {
					parts = append(parts, geminiPart{Text: textPrompt})
				}
				contents = append(contents, geminiContent{
					Role:  role,
					Parts: parts,
				})
			} else {
				contents = append(contents, geminiContent{
					Role:  role,
					Parts: []geminiPart{{Text: m.Content}},
				})
			}
		default:
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

	url := fmt.Sprintf("%s/v1beta/%s:streamGenerateContent?key=%s", a.host, modelName, a.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	go func() {
		defer close(ch)
		resp, err := a.httpClient.Do(httpReq)
		if err != nil {
			ch <- interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			var errData []map[string]interface{}
			json.Unmarshal(bodyBytes, &errData)
			ch <- interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: fmt.Errorf("google stream error %d: %v", resp.StatusCode, errData)}
			return
		}

		dec := json.NewDecoder(resp.Body)
		_, err = dec.Token()
		if err != nil {
			ch <- interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: fmt.Errorf("failed to parse stream start: %w", err)}
			return
		}

		for dec.More() {
			var chunk geminiRes
			if err := dec.Decode(&chunk); err != nil {
				ch <- interfaces.StreamEvent{Type: interfaces.EventTypeError, Err: fmt.Errorf("failed to decode chunk: %w", err)}
				return
			}

			if len(chunk.Candidates) > 0 && len(chunk.Candidates[0].Content.Parts) > 0 {
				token := chunk.Candidates[0].Content.Parts[0].Text
				ch <- interfaces.StreamEvent{Type: interfaces.EventTypeToken, Content: token}
			}
		}

		ch <- interfaces.StreamEvent{Type: interfaces.EventTypeDone}
	}()

	return ch, nil
}

func (a *Adapter) CheckHealth(ctx context.Context) error {
	if a.apiKey == "" {
		return fmt.Errorf("not configured")
	}

	url := fmt.Sprintf("%s/v1beta/models?key=%s", a.host, a.apiKey)
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

func (a *Adapter) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("Google API Key is missing")
	}

	modelName := model
	if !strings.HasPrefix(modelName, "models/") {
		modelName = "models/" + modelName
	}

	embedURL := fmt.Sprintf("%s/v1beta/%s:batchEmbedContents?key=%s", a.host, modelName, a.apiKey)

	type embedReq struct {
		Model   string `json:"model"`
		Content struct {
			Parts []map[string]string `json:"parts"`
		} `json:"content"`
	}

	var requests []embedReq
	for _, text := range texts {
		r := embedReq{Model: modelName}
		r.Content.Parts = []map[string]string{{"text": text}}
		requests = append(requests, r)
	}

	payload := map[string]interface{}{
		"requests": requests,
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
		var errData map[string]interface{}
		json.NewDecoder(resp.Body).Decode(&errData)
		return nil, fmt.Errorf("Google embed returned status: %d - %v", resp.StatusCode, errData)
	}

	var parsed struct {
		Embeddings []struct {
			Values []float32 `json:"values"`
		} `json:"embeddings"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	var results [][]float32
	for _, e := range parsed.Embeddings {
		results = append(results, e.Values)
	}
	return results, nil
}

func (a *Adapter) Discover(ctx context.Context) ([]interfaces.ModelMetadata, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("Google API Key is missing")
	}

	url := fmt.Sprintf("%s/v1beta/models?key=%s", a.host, a.apiKey)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google AI Studio returns status %d", resp.StatusCode)
	}

	var parsed geminiModelsRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	var models []interfaces.ModelMetadata
	for _, m := range parsed.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		if !strings.Contains(name, "gemini") && !strings.Contains(name, "embedding") && 
		   !strings.Contains(name, "aqa") && !strings.Contains(name, "image") && !strings.Contains(name, "imagen") {
			continue
		}

		caps := []string{"text"}
		if strings.Contains(name, "embedding") {
			caps = []string{"embedding"}
		} else if strings.Contains(name, "aqa") || strings.Contains(name, "tts") {
			caps = []string{"audio"}
		} else if strings.Contains(name, "image") || strings.Contains(name, "imagen") {
			caps = []string{"image"}
		} else if strings.Contains(name, "vision") || strings.Contains(name, "flash") || strings.Contains(name, "pro") {
			caps = append(caps, "vision")
			if strings.Contains(name, "pro") {
				caps = append(caps, "tools", "code")
			}
		}

		models = append(models, interfaces.ModelMetadata{
			ID:            name,
			DisplayName:   m.DisplayName,
			Description:   m.Description,
			ContextWindow: m.InputTokenLimit,
			Capabilities:  caps,
		})
	}
	return models, nil
}

func (a *Adapter) GetModelDetails(ctx context.Context, modelID string) (map[string]interface{}, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("Google API Key is missing")
	}

	if !strings.HasPrefix(modelID, "models/") {
		modelID = "models/" + modelID
	}

	url := fmt.Sprintf("%s/v1beta/%s?key=%s", a.host, modelID, a.apiKey)
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google AI Studio returns status %d", resp.StatusCode)
	}

	var parsed map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}

	return parsed, nil
}
