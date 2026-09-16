// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package services

import (
	"context"
	"fmt"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/interfaces"
)

type ProviderManager struct {
	repo           *db.ProviderRepository
	privacyPolicy  string
	whitelistedIPs []string
}

func NewProviderManager(repo *db.ProviderRepository, privacyPolicy string, whitelistedIPs []string) *ProviderManager {
	return &ProviderManager{repo: repo, privacyPolicy: privacyPolicy, whitelistedIPs: whitelistedIPs}
}

func (m *ProviderManager) AddProvider(ctx context.Context, name, pType, apiKey, baseURL string) (string, error) {
	// 0. Enforce Privacy Policy
	if m.privacyPolicy == "strict_local" {
		isCloud := pType == "openai" || pType == "anthropic" || pType == "google"
		if isCloud {
			return "", fmt.Errorf("network privacy policy is set to strict_local: cloud provider '%s' is blocked", pType)
		}
		if pType == "custom" && baseURL != "" {
			allowed, err := utils.IsAllowedNetwork(baseURL, m.whitelistedIPs)
			if err != nil {
				return "", fmt.Errorf("network verification failed: %w", err)
			}
			if !allowed {
				return "", fmt.Errorf("network privacy policy is set to strict_local: custom provider URL points to an unapproved public IP")
			}
		}
	}

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

	if m.privacyPolicy == "strict_local" && p.Type == "custom" && p.BaseURL != "" {
		allowed, err := utils.IsAllowedNetwork(p.BaseURL, m.whitelistedIPs)
		if err != nil {
			return fmt.Errorf("network verification failed: %w", err)
		}
		if !allowed {
			return fmt.Errorf("network privacy policy is set to strict_local: custom provider URL points to an unapproved public IP")
		}
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
	all := llm.GetSupportedProviders()
	if m.privacyPolicy == "strict_local" {
		var local []string
		for _, p := range all {
			if p == "ollama" || p == "custom" {
				local = append(local, p)
			}
		}
		return local
	}
	return all
}
