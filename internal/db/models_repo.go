// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package db

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/pkg/types"
)

// ModelRepository handles all data access related to LLM Model configurations
type ModelRepository struct {
	store  *Store
	crypto *security.CryptoService
}

// NewModelRepository instantiates the repository using the shared SQL connection and Crypto Layer
func NewModelRepository(store *Store, crypto *security.CryptoService) *ModelRepository {
	return &ModelRepository{store: store, crypto: crypto}
}

// GetActiveModels fetches user configured models sorted by highest priority and joins with provider credentials
func (r *ModelRepository) GetActiveModels() ([]types.ModelConfig, error) {
	query := `
		SELECT m.id, m.provider_id, m.alias, p.type, m.model, COALESCE(p.api_key, ''), COALESCE(p.base_url, ''), 
		       m.priority, m.is_active, COALESCE(m.capabilities, ''), COALESCE(m.context_window, 8192), COALESCE(m.use_cases, ''),
		       COALESCE(m.use_case_priorities, '{}'),
		       (CASE WHEN p.type = 'ollama' THEN (p.base_url IS NOT NULL AND p.base_url != '') ELSE (p.api_key IS NOT NULL AND p.api_key != '') END) as is_configured
		FROM models m
		JOIN providers p ON m.provider_id = p.id
		WHERE m.is_active = 1 AND p.is_active = 1
		ORDER BY m.priority ASC`

	rows, err := r.store.Conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []types.ModelConfig
	for rows.Next() {
		var m types.ModelConfig
		var encryptedKey string
		var useCasePriosStr string
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.Alias, &m.Provider, &m.Model, &encryptedKey, &m.BaseURL, &m.Priority, &m.IsActive, &m.Capabilities, &m.ContextWindow, &m.UseCases, &useCasePriosStr, &m.IsConfigured); err != nil {
			log.Printf("Failed to scan model: %v", err)
			continue
		}

		decryptedKey, err := r.crypto.Decrypt(encryptedKey)
		if err != nil {
			log.Printf("Failed to decrypt API Key for provider %s. Skipping model %s.", m.ProviderID, m.ID)
			continue
		}
		m.APIKey = decryptedKey
		hydrateCapabilities(&m)
		parseUseCasePriorities(&m, useCasePriosStr)
		models = append(models, m)
	}
	return models, nil
}

// GetAllModels fetches all models (active or inactive) for management
func (r *ModelRepository) GetAllModels() ([]types.ModelConfig, error) {
	query := `
		SELECT m.id, m.provider_id, m.alias, p.type, m.model, COALESCE(p.api_key, ''), COALESCE(p.base_url, ''), 
		       m.priority, m.is_active, COALESCE(m.capabilities, ''), COALESCE(m.context_window, 8192), COALESCE(m.use_cases, ''),
		       COALESCE(m.use_case_priorities, '{}'),
		       (CASE WHEN p.type = 'ollama' THEN (p.base_url IS NOT NULL AND p.base_url != '') ELSE (p.api_key IS NOT NULL AND p.api_key != '') END) as is_configured
		FROM models m
		JOIN providers p ON m.provider_id = p.id
		ORDER BY m.priority ASC, m.id DESC`

	rows, err := r.store.Conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []types.ModelConfig
	for rows.Next() {
		var m types.ModelConfig
		var encryptedKey string
		var useCasePriosStr string
		if err := rows.Scan(&m.ID, &m.ProviderID, &m.Alias, &m.Provider, &m.Model, &encryptedKey, &m.BaseURL, &m.Priority, &m.IsActive, &m.Capabilities, &m.ContextWindow, &m.UseCases, &useCasePriosStr, &m.IsConfigured); err != nil {
			continue
		}
		decryptedKey, _ := r.crypto.Decrypt(encryptedKey)
		m.APIKey = decryptedKey
		hydrateCapabilities(&m)
		parseUseCasePriorities(&m, useCasePriosStr)
		models = append(models, m)
	}
	return models, nil
}

// GetModelByID allows finding a model by its full ID or a prefix
func (r *ModelRepository) GetModelByID(id string) (*types.ModelConfig, error) {
	query := `
		SELECT m.id, m.provider_id, m.alias, p.type, m.model, COALESCE(p.api_key, ''), COALESCE(p.base_url, ''), 
		       m.priority, m.is_active, COALESCE(m.capabilities, ''), COALESCE(m.context_window, 8192), COALESCE(m.use_cases, ''),
		       COALESCE(m.use_case_priorities, '{}'),
		       (CASE WHEN p.type = 'ollama' THEN (p.base_url IS NOT NULL AND p.base_url != '') ELSE (p.api_key IS NOT NULL AND p.api_key != '') END) as is_configured
		FROM models m
		JOIN providers p ON m.provider_id = p.id
		WHERE m.id LIKE ? LIMIT 1`

	var m types.ModelConfig
	var encryptedKey string
	var useCasePriosStr string
	err := r.store.Conn.QueryRow(query, id+"%").Scan(&m.ID, &m.ProviderID, &m.Alias, &m.Provider, &m.Model, &encryptedKey, &m.BaseURL, &m.Priority, &m.IsActive, &m.Capabilities, &m.ContextWindow, &m.UseCases, &useCasePriosStr, &m.IsConfigured)
	if err != nil {
		return nil, err
	}

	decryptedKey, _ := r.crypto.Decrypt(encryptedKey)
	m.APIKey = decryptedKey
	hydrateCapabilities(&m)
	parseUseCasePriorities(&m, useCasePriosStr)
	return &m, nil
}

// GetAllModelsPublic returns all models without decrypting keys (faster for prompt building)
func (r *ModelRepository) GetAllModelsPublic() ([]types.ModelConfig, error) {
	query := `
		SELECT m.id, m.provider_id, m.alias, p.type, m.model, COALESCE(p.base_url, ''), 
		       m.priority, m.is_active, COALESCE(m.capabilities, ''), COALESCE(m.context_window, 8192), COALESCE(m.use_cases, ''),
		       COALESCE(m.use_case_priorities, '{}'),
		       (CASE WHEN p.type = 'ollama' THEN (p.base_url IS NOT NULL AND p.base_url != '') ELSE (p.api_key IS NOT NULL AND p.api_key != '') END) as is_configured
		FROM models m
		JOIN providers p ON m.provider_id = p.id
		ORDER BY m.priority ASC`

	rows, err := r.store.Conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []types.ModelConfig
	for rows.Next() {
		var m types.ModelConfig
		var useCasePriosStr string
		err := rows.Scan(&m.ID, &m.ProviderID, &m.Alias, &m.Provider, &m.Model, &m.BaseURL, &m.Priority, &m.IsActive, &m.Capabilities, &m.ContextWindow, &m.UseCases, &useCasePriosStr, &m.IsConfigured)
		if err != nil {
			return nil, err
		}
		hydrateCapabilities(&m)
		parseUseCasePriorities(&m, useCasePriosStr)
		models = append(models, m)
	}
	return models, nil
}

// shiftPriorities opens a slot at priority P by incrementing all models from P onwards
func (r *ModelRepository) shiftPriorities(id string, p int) error {
	query := "UPDATE models SET priority = priority + 1 WHERE priority >= ? AND id != ?"
	_, err := r.store.Conn.Exec(query, p, id)
	return err
}

// UpsertModel persists the model configuration linked to a provider
func (r *ModelRepository) UpsertModel(m types.ModelConfig) error {
	// Open priority slot if needed
	_ = r.shiftPriorities(m.ID, m.Priority)

	query := `INSERT INTO models (id, provider_id, alias, model, priority, is_active, capabilities, context_window, use_cases, use_case_priorities)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(id) DO UPDATE SET
	          provider_id=excluded.provider_id, alias=excluded.alias, model=excluded.model,
	          priority=excluded.priority, is_active=excluded.is_active,
			  capabilities=excluded.capabilities, context_window=excluded.context_window, use_cases=excluded.use_cases, use_case_priorities=excluded.use_case_priorities`

	fmt.Printf("Model Provider ID: %v\n", m.ProviderID)
	
	// Marshal use case priorities
	ucb, _ := json.Marshal(m.UseCasePriorities)
	if string(ucb) == "null" {
		ucb = []byte("{}")
	}

	_, err := r.store.Conn.Exec(query, m.ID, m.ProviderID, m.Alias, m.Model, m.Priority, m.IsActive, m.Capabilities, m.ContextWindow, m.UseCases, string(ucb))
	return err
}

// DeleteModel permanently removes a model by ID prefix or full ID
func (r *ModelRepository) DeleteModel(id string) error {
	res, err := r.store.Conn.Exec("DELETE FROM models WHERE id LIKE ?", id+"%")
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("no model matched id prefix '%s'", id)
	}
	return nil
}

// SetActive toggles the is_active flag on a model
func (r *ModelRepository) SetActive(id string, active bool) error {
	res, err := r.store.Conn.Exec("UPDATE models SET is_active = ? WHERE id LIKE ?", active, id+"%")
	if err != nil {
		return err
	}
	affected, _ := res.RowsAffected()
	if affected == 0 {
		return fmt.Errorf("no model matched id prefix '%s'", id)
	}
	return nil
}

func hydrateCapabilities(m *types.ModelConfig) {
	// Only infer if completely empty (legacy or just added without details)
	if m.Capabilities == "" {
		mname := strings.ToLower(m.Model)
		var caps []string
		caps = append(caps, "text")

		if strings.Contains(mname, "vision") || strings.Contains(mname, "gemini-1.5") || strings.Contains(mname, "gemini-2") || strings.Contains(mname, "gpt-4o") || strings.Contains(mname, "claude-3-5") || strings.Contains(mname, "llava") {
			caps = append(caps, "vision")
		}
		
		if strings.Contains(mname, "gemini-1.5") || strings.Contains(mname, "gemini-2") || strings.Contains(mname, "gpt-4o") {
			caps = append(caps, "audio")
		}

		// Tool calling assumption: most modern clouds and llama3.1+ do
		if m.Provider == "openai" || m.Provider == "anthropic" || m.Provider == "google" || strings.Contains(mname, "llama3.1") || strings.Contains(mname, "llama3.2") || strings.Contains(mname, "llama3.3") {
			caps = append(caps, "tools")
		}

		m.Capabilities = strings.Join(caps, ",")
	}

	if m.ContextWindow == 0 || m.ContextWindow == 8192 {
		mname := strings.ToLower(m.Model)
		if m.Provider == "google" || m.Provider == "gemini" {
			m.ContextWindow = 1000000 // default gemini
		} else if m.Provider == "anthropic" {
			m.ContextWindow = 200000
		} else if strings.Contains(mname, "gpt-4") {
			m.ContextWindow = 128000
		} else {
			m.ContextWindow = 8192 // fallback for standard ollama models mostly
		}
	}
}

func parseUseCasePriorities(m *types.ModelConfig, raw string) {
	m.UseCasePriorities = make(map[string]int)
	if raw == "" || raw == "{}" {
		return
	}
	_ = json.Unmarshal([]byte(raw), &m.UseCasePriorities)
}
