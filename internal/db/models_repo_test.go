// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package db

import (
	"path/filepath"
	"testing"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/pkg/types"
)

func setupTestCrypto(t *testing.T) *security.CryptoService {
	tempDir := t.TempDir()
	keyPath := filepath.Join(tempDir, "test.key")
	crypto, err := security.NewCryptoService(keyPath)
	if err != nil {
		t.Fatalf("Failed to setup crypto: %v", err)
	}
	return crypto
}

func TestModelsRepo_UpsertAndGet(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()
	crypto := setupTestCrypto(t)
	providerRepo := NewProviderRepository(store, crypto)
	providerRepo.Create(&Provider{
		ID:       "openai",
		Name:     "OpenAI",
		Type:     "openai",
		APIKey:   "secret-key",
		IsActive: true,
	})
	repo := NewModelRepository(store, crypto)

	model := types.ModelConfig{
		ID:           "test-model",
		ProviderID:   "openai",
		Provider:     "openai",
		Model:        "gpt-4",
		Alias:        "GPT-4",
		APIKey:       "secret-key",
		Priority:     1,
		IsActive:     true,
		Capabilities: "chat,vision",
	}

	err := repo.UpsertModel(model)
	if err != nil {
		t.Fatalf("Failed to upsert model: %v", err)
	}

	m, err := repo.GetModelByID("test-model")
	if err != nil {
		t.Fatalf("Failed to get model: %v", err)
	}

	if m.APIKey != "secret-key" {
		t.Errorf("Expected decrypted API key 'secret-key', got '%s'", m.APIKey)
	}
	if m.Capabilities != "chat,vision" {
		t.Errorf("Expected capabilities to be parsed properly, got %v", m.Capabilities)
	}

	active, err := repo.GetActiveModels()
	if err != nil || len(active) != 1 {
		t.Fatalf("Expected 1 active model, got %d", len(active))
	}

	err = repo.SetActive("test-model", false)
	if err != nil {
		t.Fatalf("Failed to set active: %v", err)
	}

	active, _ = repo.GetActiveModels()
	if len(active) != 0 {
		t.Errorf("Expected 0 active models after deactivation, got %d", len(active))
	}
}

func TestModelsRepo_ShiftPriorities(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()
	crypto := setupTestCrypto(t)
	providerRepo := NewProviderRepository(store, crypto)
	providerRepo.Create(&Provider{ID: "p1", Name: "Local", Type: "local", IsActive: true})
	repo := NewModelRepository(store, crypto)

	repo.UpsertModel(types.ModelConfig{ID: "m1", Model: "m1", ProviderID: "p1", Priority: 1, IsActive: true})
	repo.UpsertModel(types.ModelConfig{ID: "m2", Model: "m2", ProviderID: "p1", Priority: 2, IsActive: true})
	
	// Test the Upsert mechanism that calls shiftPriorities implicitly
	repo.UpsertModel(types.ModelConfig{ID: "m3", Model: "m3", ProviderID: "p1", Priority: 1, IsActive: true})
	
	m1, err := repo.GetModelByID("m1")
	if err != nil {
		t.Fatalf("Failed to get m1: %v", err)
	}
	if m1.Priority != 2 {
		t.Errorf("Expected m1 priority to be shifted to 2, got %d", m1.Priority)
	}

	m2, err := repo.GetModelByID("m2")
	if err != nil {
		t.Fatalf("Failed to get m2: %v", err)
	}
	if m2.Priority != 3 {
		t.Errorf("Expected m2 priority to be shifted to 3, got %d", m2.Priority)
	}
}
