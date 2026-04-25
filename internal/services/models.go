package services

import (
	"context"
	"fmt"
	"github.com/google/uuid"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/pkg/types"
)

// ModelService bridges UI/CLI input with actual Provider Health validations and DB storage
type ModelService struct {
	repo *db.ModelRepository
}

func NewModelService(repo *db.ModelRepository) *ModelService {
	return &ModelService{repo: repo}
}

// AddAndVerifyModel tries to instantiate the target LLM and bounces a health check before permitting DB insertion.
func (s *ModelService) AddAndVerifyModel(ctx context.Context, provider, modelName, apiKey, baseURL string, priority int, useCases string) error {
	adapter, err := s.getAdapter(provider, apiKey, baseURL)
	if err != nil {
		return err
	}

	// The ultimate gatekeeper (Validates real token vs fake formats)
	if err := adapter.CheckHealth(ctx); err != nil {
		return fmt.Errorf("health check failed, the provider rejected the connection or API key: %w", err)
	}

	// Success, we write it to Database (Encrypted safely by Repo)
	cfg := types.ModelConfig{
		ID:       uuid.New().String(),
		Alias:    fmt.Sprintf("%s (%s)", provider, modelName),
		Provider: provider,
		Model:    modelName,
		APIKey:   apiKey,
		BaseURL:  baseURL,
		Priority: priority,
		IsActive: true,
		UseCases: useCases,
	}

	if err := s.repo.UpsertModel(cfg); err != nil {
		return fmt.Errorf("failed persisting the valid model to the Repository: %w", err)
	}

	return nil
}

// ListModels returns all configured models
func (s *ModelService) ListModels() ([]types.ModelConfig, error) {
	return s.repo.GetAllModels()
}

// UpdateModel allows changing specific fields of a model identified by its ID (or short ID)
func (s *ModelService) UpdateModel(ctx context.Context, id string, alias, modelName, apiKey *string, priority *int, isActive *bool, useCases *string) error {
	m, err := s.repo.GetModelByID(id)
	if err != nil {
		return fmt.Errorf("model with ID prefix %s not found: %w", id, err)
	}

	if alias != nil {
		m.Alias = *alias
	}
	if modelName != nil {
		m.Model = *modelName
	}
	if priority != nil {
		m.Priority = *priority
	}
	if isActive != nil {
		m.IsActive = *isActive
	}
	if useCases != nil {
		m.UseCases = *useCases
	}

	if apiKey != nil {
		m.APIKey = *apiKey
		// Re-verify health if API Key changes
		adapter, err := s.getAdapter(m.Provider, m.APIKey, m.BaseURL)
		if err == nil {
			if err := adapter.CheckHealth(ctx); err != nil {
				return fmt.Errorf("updated API Key health check failed: %w", err)
			}
		}
	}

	return s.repo.UpsertModel(*m)
}

// CheckModelHealth verifies if an existing model's configuration is still valid
func (s *ModelService) CheckModelHealth(ctx context.Context, id string) error {
	m, err := s.repo.GetModelByID(id)
	if err != nil {
		return fmt.Errorf("model not found: %w", err)
	}

	adapter, err := s.getAdapter(m.Provider, m.APIKey, m.BaseURL)
	if err != nil {
		return err
	}

	return adapter.CheckHealth(ctx)
}

func (s *ModelService) getAdapter(provider, apiKey, baseURL string) (llm.Provider, error) {
	switch provider {
	case "openai":
		return llm.NewOpenAIAdapter(apiKey), nil
	case "anthropic":
		return llm.NewAnthropicAdapter(apiKey), nil
	case "ollama":
		return llm.NewOllamaAdapter(baseURL), nil
	case "gemini":
		return llm.NewGeminiAdapter(apiKey), nil
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}
}

