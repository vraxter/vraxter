package db

import (
	"testing"
)

func TestProviderRepo_CreateAndGet(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()
	crypto := setupTestCrypto(t)
	repo := NewProviderRepository(store, crypto)

	provider := Provider{
		ID:     "prov-1",
		Name:   "OpenAI",
		Type:   "openai",
		APIKey: "sk-test-123",
	}

	err := repo.Create(&provider)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	p, err := repo.GetByIDOrName("prov-1")
	if err != nil {
		t.Fatalf("Failed to get provider: %v", err)
	}
	if p.APIKey != "sk-test-123" {
		t.Errorf("Expected API key 'sk-test-123', got '%s'", p.APIKey)
	}

	p2, err := repo.GetByIDOrName("OpenAI")
	if err != nil {
		t.Fatalf("Failed to get provider by name: %v", err)
	}
	if p2.ID != "prov-1" {
		t.Errorf("Expected ID 'prov-1', got '%s'", p2.ID)
	}

	// Test Update
	p.APIKey = "sk-new-456"
	err = repo.Update(p)
	if err != nil {
		t.Fatalf("Failed to update provider: %v", err)
	}

	p3, _ := repo.GetByIDOrName("prov-1")
	if p3.APIKey != "sk-new-456" {
		t.Errorf("Expected updated API key 'sk-new-456', got '%s'", p3.APIKey)
	}

	// Test Delete
	err = repo.Delete("prov-1")
	if err != nil {
		t.Fatalf("Failed to delete provider: %v", err)
	}

	_, err = repo.GetByIDOrName("prov-1")
	if err == nil {
		t.Error("Expected error getting deleted provider, got nil")
	}
}
