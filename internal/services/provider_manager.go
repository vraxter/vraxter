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
	"strings"
	"sync"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/interfaces"
)

type ProviderManager struct {
	mu             sync.RWMutex
	repo           *db.ProviderRepository
	privacyPolicy  string
	whitelistedIPs []string
	isolationMode  string
}

func NewProviderManager(repo *db.ProviderRepository, privacyPolicy string, whitelistedIPs []string, isolationMode ...string) *ProviderManager {
	iso := "airgap"
	if len(isolationMode) > 0 && strings.TrimSpace(isolationMode[0]) != "" {
		iso = strings.ToLower(strings.TrimSpace(isolationMode[0]))
	} else if privacyPolicy == "strict_local" {
		iso = "airgap"
	} else if privacyPolicy == "custom" || privacyPolicy == "ask" {
		iso = "hybrid"
	}
	wlCopy := append([]string(nil), whitelistedIPs...)
	return &ProviderManager{
		repo:           repo,
		privacyPolicy:  privacyPolicy,
		whitelistedIPs: wlCopy,
		isolationMode:  iso,
	}
}

// UpdatePolicy synchronizes isolation mode, privacy policy, and whitelisted IPs safely at runtime.
func (m *ProviderManager) UpdatePolicy(isolationMode, privacyPolicy string, whitelistedIPs []string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if strings.TrimSpace(isolationMode) != "" {
		m.isolationMode = strings.ToLower(strings.TrimSpace(isolationMode))
	}
	if strings.TrimSpace(privacyPolicy) != "" {
		m.privacyPolicy = privacyPolicy
	}
	m.whitelistedIPs = append([]string(nil), whitelistedIPs...)
}

// IsolationMode returns the active isolation mode enforced by this ProviderManager.
func (m *ProviderManager) IsolationMode() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.isolationMode
}

func (m *ProviderManager) policySnapshot() (mode string, privacy string, wl []string, restricted bool) {
	m.mu.RLock()
	mode = m.isolationMode
	privacy = m.privacyPolicy
	wl = append([]string(nil), m.whitelistedIPs...)
	m.mu.RUnlock()
	restricted = mode == "airgap" || mode == "strict_local" || mode == "local_lan" || privacy == "strict_local"
	return
}

func isCloudProviderType(pType string) bool {
	p := strings.ToLower(strings.TrimSpace(pType))
	return p == "openai" || p == "anthropic" || p == "google"
}

func (m *ProviderManager) validateProviderPolicy(pType, baseURL string) error {
	mode, _, wl, restricted := m.policySnapshot()
	if !restricted {
		return nil
	}

	if isCloudProviderType(pType) {
		return fmt.Errorf("isolation mode is set to %s: cloud provider '%s' is blocked", mode, pType)
	}
	if (pType == "custom" || pType == "ollama") && baseURL != "" {
		allowed, err := utils.IsAllowedNetworkWithMode(baseURL, mode, wl)
		if err != nil || !allowed {
			return fmt.Errorf("isolation mode is set to %s: provider URL points to an unapproved public IP (%v)", mode, err)
		}
	}
	return nil
}

func (m *ProviderManager) AddProvider(ctx context.Context, name, pType, apiKey, baseURL string) (string, error) {
	pType = strings.ToLower(strings.TrimSpace(pType))
	baseURL = strings.TrimSpace(baseURL)
	if pType == "ollama" && baseURL == "" {
		baseURL = "http://localhost:11434"
	}
	if pType == "custom" && baseURL == "" {
		return "", fmt.Errorf("custom provider requires a base URL")
	}

	// 0. Enforce Isolation Policy (airgap AND local_lan)
	if err := m.validateProviderPolicy(pType, baseURL); err != nil {
		return "", err
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
	all, err := m.repo.GetAll()
	if err != nil {
		return nil, err
	}
	_, _, _, restricted := m.policySnapshot()
	if !restricted {
		return all, nil
	}
	var allowed []db.Provider
	for _, p := range all {
		if m.validateProviderPolicy(p.Type, p.BaseURL) == nil {
			allowed = append(allowed, p)
		}
	}
	return allowed, nil
}

func (m *ProviderManager) DiscoverModels(ctx context.Context, idOrName string) ([]interfaces.ModelMetadata, error) {
	p, err := m.repo.GetByIDOrName(idOrName)
	if err != nil {
		return nil, fmt.Errorf("provider '%s' is not configured yet. Use 'vraxter providers setup' to add it", idOrName)
	}

	if err := m.validateProviderPolicy(p.Type, p.BaseURL); err != nil {
		return nil, err
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

	if err := m.validateProviderPolicy(p.Type, p.BaseURL); err != nil {
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

	if err := m.validateProviderPolicy(p.Type, p.BaseURL); err != nil {
		return err
	}

	// Verify new config
	adapter, err := llm.Create(p.Type, p.APIKey, p.BaseURL)
	if err != nil {
		return err
	}
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

// GetSupportedProviders returns a list of all provider types permitted under the active isolation mode.
func (m *ProviderManager) GetSupportedProviders() []string {
	all := llm.GetSupportedProviders()
	_, _, _, restricted := m.policySnapshot()
	if restricted {
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
