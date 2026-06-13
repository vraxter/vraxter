package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/patagonicrune/vraxter/internal/llm/registry"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
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

type Adapter struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

func NewAdapter(apiKey, host string) *Adapter {
	if host == "" {
		host = "https://api.openai.com/v1/chat/completions"
	}
	if !strings.Contains(host, "/chat/completions") && !strings.Contains(host, "/v1") {
		host = strings.TrimSuffix(host, "/") + "/v1/chat/completions"
	}

	return &Adapter{
		apiKey:     apiKey,
		baseURL:    host,
		httpClient: &http.Client{},
	}
}

func (a *Adapter) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
	if a.apiKey == "" {
		return interfaces.CompletionResponse{}, fmt.Errorf("OpenAI API Key is missing")
	}

	payload := openaiReq{
		Model:    req.Model,
		Messages: make([]openaiMessage, len(req.Messages)),
	}

	for i, m := range req.Messages {
		if mimeType, b64Data, textPrompt, ok := interfaces.ParseDataURI(m.Content); ok {
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
				if textPrompt != "" {
					parts = append(parts, map[string]interface{}{
						"type": "text",
						"text": textPrompt,
					})
				}
				payload.Messages[i] = openaiMessage{
					Role:    m.Role,
					Content: parts,
				}
			} else {
				parts := []interface{}{
					map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]string{
							"url": fmt.Sprintf("data:%s;base64,%s", mimeType, b64Data),
						},
					},
				}
				if textPrompt != "" {
					parts = append(parts, map[string]interface{}{
						"type": "text",
						"text": textPrompt,
					})
				}
				payload.Messages[i] = openaiMessage{
					Role:    m.Role,
					Content: parts,
				}
			}
		} else {
			payload.Messages[i] = openaiMessage{
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
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

	resp, err := a.httpClient.Do(httpReq)
	if err != nil {
		return interfaces.CompletionResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return interfaces.CompletionResponse{}, fmt.Errorf("OpenAI returned status: %s", resp.Status)
	}

	var parsed openaiRes
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return interfaces.CompletionResponse{}, err
	}

	if len(parsed.Choices) == 0 {
		return interfaces.CompletionResponse{}, fmt.Errorf("no choices returned by OpenAI")
	}

	return interfaces.CompletionResponse{Content: parsed.Choices[0].Message.Content}, nil
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

func (a *Adapter) SynthesizeSpeech(ctx context.Context, text, voice string) ([]byte, error) {
	if a.apiKey == "" {
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

	url := strings.Replace(a.baseURL, "chat/completions", "audio/speech", 1)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

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
	if a.apiKey == "" {
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

	url := strings.Replace(a.baseURL, "chat/completions", "audio/transcriptions", 1)
	req, err := http.NewRequestWithContext(ctx, "POST", url, body)
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+a.apiKey)

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

func (a *Adapter) Discover(ctx context.Context) ([]interfaces.ModelMetadata, error) {
	if a.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API Key is missing")
	}

	reqURL := "https://api.openai.com/v1/models"
	httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

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
		caps := []string{"text"}
		if strings.Contains(m.ID, "embedding") {
			caps = []string{"embedding"}
		} else if strings.Contains(m.ID, "dall-e") {
			caps = []string{"image"}
		} else if strings.Contains(m.ID, "tts") || strings.Contains(m.ID, "whisper") {
			caps = []string{"audio"}
		} else if !strings.HasPrefix(m.ID, "gpt-") && !strings.HasPrefix(m.ID, "o1-") {
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
	if a.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API Key is missing")
	}

	reqURL := "https://api.openai.com/v1/models/" + modelID
	httpReq, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+a.apiKey)

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
