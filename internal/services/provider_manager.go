package services

import (
	"context"
	"fmt"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/utils"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
)

type ProviderManager struct {
	repo *db.ProviderRepository
}

func NewProviderManager(repo *db.ProviderRepository) *ProviderManager {
	return &ProviderManager{repo: repo}
}

func (m *ProviderManager) AddProvider(ctx context.Context, name, pType, apiKey, baseURL string) (string, error) {
	// 1. Validate credentials via dynamic Registry + healthcheck
	adapter, err := llm.Create(pType, apiKey, baseURL)
	if err != nil {
		return "", err
	}

	if err := adapter.CheckHealth(ctx); err != nil {
		return "", fmt.Errorf("connection test failed: %w", err)
	}

	// 2. Persist
	id := utils.GenerateShortID(name)
	provider := &db.Provider{
		ID:       id,
		Name:     name,
		Type:     pType,
		APIKey:   apiKey,
		BaseURL:  baseURL,
		IsActive: true,
	}

	if err := m.repo.Create(provider); err != nil {
		return "", fmt.Errorf("failed to save provider: %w", err)
	}

	return id, nil
}

func (m *ProviderManager) ListProviders() ([]db.Provider, error) {
	return m.repo.GetAll()
}

func (m *ProviderManager) DiscoverModels(ctx context.Context, idOrName string) ([]interfaces.ModelMetadata, error) {
	p, err := m.repo.GetByIDOrName(idOrName)
	if err != nil {
		return nil, fmt.Errorf("provider '%s' is not configured yet. Use 'vraxter providers setup' to add it", idOrName)
	}

	adapter, err := llm.Create(p.Type, p.APIKey, p.BaseURL)
	if err != nil {
		return nil, err
	}

	return adapter.Discover(ctx)
}

func (m *ProviderManager) GetModelDetails(ctx context.Context, providerIDOrName, modelID string) (map[string]interface{}, error) {
	p, err := m.repo.GetByIDOrName(providerIDOrName)
	if err != nil {
		return nil, err
	}

	adapter, err := llm.Create(p.Type, p.APIKey, p.BaseURL)
	if err != nil {
		return nil, err
	}

	return adapter.GetModelDetails(ctx, modelID)
}

func (m *ProviderManager) GetProvider(idOrName string) (*db.Provider, error) {
	return m.repo.GetByIDOrName(idOrName)
}

func (m *ProviderManager) UpdateProvider(ctx context.Context, idOrName string, name, apiKey, baseURL *string) error {
	p, err := m.repo.GetByIDOrName(idOrName)
	if err != nil {
		return err
	}

	if name != nil {
		p.Name = *name
	}
	if apiKey != nil {
		p.APIKey = *apiKey
	}
	if baseURL != nil {
		p.BaseURL = *baseURL
	}

	// Verify new config
	adapter, _ := llm.Create(p.Type, p.APIKey, p.BaseURL)
	if err := adapter.CheckHealth(ctx); err != nil {
		return fmt.Errorf("updated configuration failed health check: %w", err)
	}

	return m.repo.Update(p)
}

func (m *ProviderManager) DeleteProvider(id string) error {
	return m.repo.Delete(id)
}

func (m *ProviderManager) ConfigureProvider(ctx context.Context, name, pType, apiKey, baseURL string) (string, error) {
	return m.AddProvider(ctx, name, pType, apiKey, baseURL)
}

// GetSupportedProviders returns a list of all provider types available in the registry
func (m *ProviderManager) GetSupportedProviders() []string {
	return llm.GetSupportedProviders()
}
