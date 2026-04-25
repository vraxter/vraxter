package db

import (
	"fmt"
	"log"
	"strings"

	"github.com/patagonicrune/vraxter/internal/security"
	"github.com/patagonicrune/vraxter/pkg/types"
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

// GetActiveModels fetches user configured models sorted by highest priority and decrypts API keys on the fly
func (r *ModelRepository) GetActiveModels() ([]types.ModelConfig, error) {
	query := "SELECT id, alias, provider, model, COALESCE(api_key, ''), COALESCE(base_url, ''), priority, is_active, COALESCE(capabilities, ''), COALESCE(context_window, 8192), COALESCE(use_cases, '') FROM models WHERE is_active = 1 ORDER BY priority ASC"
	rows, err := r.store.Conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []types.ModelConfig
	for rows.Next() {
		var m types.ModelConfig
		var encryptedKey string
		if err := rows.Scan(&m.ID, &m.Alias, &m.Provider, &m.Model, &encryptedKey, &m.BaseURL, &m.Priority, &m.IsActive, &m.Capabilities, &m.ContextWindow, &m.UseCases); err != nil {
			log.Printf("Failed to scan model: %v", err)
			continue
		}

		// Decrypt Data At Rest
		decryptedKey, err := r.crypto.Decrypt(encryptedKey)
		if err != nil {
			log.Printf("Failed to decrypt API Key for model %s. Is the master key correct? Skipping...", m.ID)
			continue
		}
		m.APIKey = decryptedKey
		m.IsActive = true
		hydrateCapabilities(&m)
		models = append(models, m)
	}
	return models, nil
}

// GetAllModels fetches all models (active or inactive) for management
func (r *ModelRepository) GetAllModels() ([]types.ModelConfig, error) {
	query := "SELECT id, alias, provider, model, COALESCE(api_key, ''), COALESCE(base_url, ''), priority, is_active, COALESCE(capabilities, ''), COALESCE(context_window, 8192), COALESCE(use_cases, '') FROM models ORDER BY priority ASC, id DESC"
	rows, err := r.store.Conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []types.ModelConfig
	for rows.Next() {
		var m types.ModelConfig
		var encryptedKey string
		if err := rows.Scan(&m.ID, &m.Alias, &m.Provider, &m.Model, &encryptedKey, &m.BaseURL, &m.Priority, &m.IsActive, &m.Capabilities, &m.ContextWindow, &m.UseCases); err != nil {
			log.Printf("Failed to scan model: %v", err)
			continue
		}
		decryptedKey, _ := r.crypto.Decrypt(encryptedKey)
		m.APIKey = decryptedKey
		hydrateCapabilities(&m)
		models = append(models, m)
	}
	return models, nil
}

// GetAllModelsPublic returns all models without decrypting keys (faster for prompt building)
func (r *ModelRepository) GetAllModelsPublic() ([]types.ModelConfig, error) {
	query := "SELECT id, alias, provider, model, COALESCE(base_url, ''), priority, is_active, COALESCE(capabilities, ''), COALESCE(context_window, 8192), COALESCE(use_cases, '') FROM models ORDER BY priority ASC"
	rows, err := r.store.Conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var models []types.ModelConfig
	for rows.Next() {
		var m types.ModelConfig
		err := rows.Scan(&m.ID, &m.Alias, &m.Provider, &m.Model, &m.BaseURL, &m.Priority, &m.IsActive, &m.Capabilities, &m.ContextWindow, &m.UseCases)
		if err != nil {
			return nil, err
		}
		hydrateCapabilities(&m)
		models = append(models, m)
	}
	return models, nil
}

// GetModelByID allows finding a model by its full ID or a prefix (e.g., first 6-8 chars)
func (r *ModelRepository) GetModelByID(id string) (*types.ModelConfig, error) {
	query := "SELECT id, alias, provider, model, COALESCE(api_key, ''), COALESCE(base_url, ''), priority, is_active, COALESCE(capabilities, ''), COALESCE(context_window, 8192), COALESCE(use_cases, '') FROM models WHERE id LIKE ? LIMIT 1"
	var m types.ModelConfig
	var encryptedKey string
	err := r.store.Conn.QueryRow(query, id+"%").Scan(&m.ID, &m.Alias, &m.Provider, &m.Model, &encryptedKey, &m.BaseURL, &m.Priority, &m.IsActive, &m.Capabilities, &m.ContextWindow, &m.UseCases)
	if err != nil {
		return nil, err
	}

	decryptedKey, _ := r.crypto.Decrypt(encryptedKey)
	m.APIKey = decryptedKey
	hydrateCapabilities(&m)
	return &m, nil
}

// shiftPriorities opens a slot at priority P by incrementing all models from P onwards
func (r *ModelRepository) shiftPriorities(id string, p int) error {
	query := "UPDATE models SET priority = priority + 1 WHERE priority >= ? AND id != ?"
	_, err := r.store.Conn.Exec(query, p, id)
	return err
}

// UpsertModel safely encrypts the API key and persists the provider configuration
func (r *ModelRepository) UpsertModel(m types.ModelConfig) error {
	// Open priority slot if needed
	if err := r.shiftPriorities(m.ID, m.Priority); err != nil {
		log.Printf("Warning: priority shift failed: %v", err)
	}

	encryptedKey, err := r.crypto.Encrypt(m.APIKey)
	if err != nil {
		return err
	}

	query := `INSERT INTO models (id, alias, provider, model, api_key, base_url, priority, is_active, capabilities, context_window, use_cases)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(id) DO UPDATE SET
	          alias=excluded.alias, provider=excluded.provider, model=excluded.model,
	          api_key=excluded.api_key, base_url=excluded.base_url, priority=excluded.priority, is_active=excluded.is_active,
			  capabilities=excluded.capabilities, context_window=excluded.context_window, use_cases=excluded.use_cases`

	_, err = r.store.Conn.Exec(query, m.ID, m.Alias, m.Provider, m.Model, encryptedKey, m.BaseURL, m.Priority, m.IsActive, m.Capabilities, m.ContextWindow, m.UseCases)
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
		
		// Tool calling assumption: most modern clouds and llama3.1+ do
		if m.Provider == "openai" || m.Provider == "anthropic" || m.Provider == "gemini" || strings.Contains(mname, "llama3.1") || strings.Contains(mname, "llama3.2") || strings.Contains(mname, "llama3.3") {
			caps = append(caps, "tools")
		}

		m.Capabilities = strings.Join(caps, ",")
	}

	if m.ContextWindow == 0 || m.ContextWindow == 8192 {
		mname := strings.ToLower(m.Model)
		if m.Provider == "gemini" {
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


