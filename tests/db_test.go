// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package tests

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/pkg/types"
)

// newTestStore boots a clean in-memory SQLite DB for each test.
func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// newTestCrypto creates a CryptoService with a fresh test key file.
func newTestCrypto(t *testing.T) *security.CryptoService {
	t.Helper()
	tmp := t.TempDir()
	keyPath := tmp + "/.testmasterkey"
	cs, err := security.NewCryptoService(keyPath)
	if err != nil {
		t.Fatalf("failed to create crypto service: %v", err)
	}
	return cs
}

// ─── SQLite Store ────────────────────────────────────────────────────────────

func TestStore_InitAndPing(t *testing.T) {
	store := newTestStore(t)
	if err := store.Conn.Ping(); err != nil {
		t.Fatalf("expected store to be healthy: %v", err)
	}
}

// ─── Chat Repository ─────────────────────────────────────────────────────────

func TestChatRepo_CreateAndFindConversation(t *testing.T) {
	store := newTestStore(t)
	repo := db.NewChatRepository(store)

	conv := &types.Conversation{
		ID:    uuid.NewString(),
		Title: "Test Chat",
	}

	if err := repo.CreateConversation(conv); err != nil {
		t.Fatalf("CreateConversation failed: %v", err)
	}

	found, err := repo.FindConversation(conv.ID)
	if err != nil || found == nil {
		t.Fatalf("FindConversation failed: err=%v, found=%v", err, found)
	}
	if found.Title != conv.Title {
		t.Errorf("expected title %q, got %q", conv.Title, found.Title)
	}
}

func TestChatRepo_FindConversation_NotFound(t *testing.T) {
	store := newTestStore(t)
	repo := db.NewChatRepository(store)

	found, err := repo.FindConversation("nonexistent-id")
	if err != nil {
		t.Fatalf("expected nil error for missing conv, got: %v", err)
	}
	if found != nil {
		t.Fatal("expected nil result for missing conversation")
	}
}

func TestChatRepo_SaveAndGetMessages(t *testing.T) {
	store := newTestStore(t)
	repo := db.NewChatRepository(store)

	convID := uuid.NewString()
	if err := repo.CreateConversation(&types.Conversation{ID: convID, Title: "msgs"}); err != nil {
		t.Fatal(err)
	}

	msgs := []types.Message{
		{ID: uuid.NewString(), ConversationID: convID, Role: "user", Content: "hello world", TokensUsed: 5},
		{ID: uuid.NewString(), ConversationID: convID, Role: "assistant", Content: "hi there", TokensUsed: 3},
		{ID: uuid.NewString(), ConversationID: convID, Role: "user", Content: "thanks", TokensUsed: 2},
	}

	for _, m := range msgs {
		if err := repo.SaveMessage(&m); err != nil {
			t.Fatalf("SaveMessage failed: %v", err)
		}
		time.Sleep(2 * time.Millisecond) // Ensure distinct timestamps in SQLite
	}

	history, err := repo.GetMessagesByConversation(convID, 10)
	if err != nil {
		t.Fatalf("GetMessagesByConversation failed: %v", err)
	}
	if len(history) != 3 {
		t.Errorf("expected 3 messages, got %d", len(history));	return
	}
	// Verify we got the right messages regardless of exact ordering
	contents := map[string]bool{}
	for _, m := range history {
		contents[m.Content] = true
	}
	if !contents["hello world"] || !contents["hi there"] || !contents["thanks"] {
		t.Errorf("missing expected messages, got: %+v", history)
	}
}

func TestChatRepo_GetMessages_WithLimit(t *testing.T) {
	store := newTestStore(t)
	repo := db.NewChatRepository(store)

	convID := uuid.NewString()
	repo.CreateConversation(&types.Conversation{ID: convID, Title: "limit test"})

	for i := 0; i < 5; i++ {
		repo.SaveMessage(&types.Message{
			ID: uuid.NewString(), ConversationID: convID,
			Role: "user", Content: "msg",
		})
		time.Sleep(1 * time.Millisecond)
	}

	limited, err := repo.GetMessagesByConversation(convID, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(limited) != 2 {
		t.Errorf("expected 2 messages with limit=2, got %d", len(limited))
	}
}

func TestChatRepo_UpdateSummary(t *testing.T) {
	store := newTestStore(t)
	repo := db.NewChatRepository(store)

	convID := uuid.NewString()
	repo.CreateConversation(&types.Conversation{ID: convID, Title: "summary test"})

	if err := repo.UpdateConversationSummary(convID, "This was a great conversation"); err != nil {
		t.Fatalf("UpdateConversationSummary failed: %v", err)
	}

	conv, _ := repo.FindConversation(convID)
	if conv.Summary != "This was a great conversation" {
		t.Errorf("expected summary to be updated, got %q", conv.Summary)
	}
}

func TestChatRepo_TruncateOldMessages(t *testing.T) {
	store := newTestStore(t)
	repo := db.NewChatRepository(store)

	convID := uuid.NewString()
	repo.CreateConversation(&types.Conversation{ID: convID, Title: "truncate test"})

	for i := 0; i < 5; i++ {
		repo.SaveMessage(&types.Message{
			ID: uuid.NewString(), ConversationID: convID,
			Role: "user", Content: "message",
		})
		time.Sleep(1 * time.Millisecond)
	}

	if err := repo.TruncateOldMessages(convID, 2); err != nil {
		t.Fatalf("TruncateOldMessages failed: %v", err)
	}

	remaining, _ := repo.GetMessagesByConversation(convID, 100)
	if len(remaining) != 2 {
		t.Errorf("expected 2 messages after truncation, got %d", len(remaining))
	}
}

func TestChatRepo_FindLatestConversationBySpecialist(t *testing.T) {
	store := newTestStore(t)
	repo := db.NewChatRepository(store)

	specialistID := "spec-golang"
	conv1ID := uuid.NewString()
	conv2ID := uuid.NewString()

	// Insert first then second with explicit different timestamps via direct SQL
	store.Conn.Exec("INSERT INTO conversations (id, title, specialist_id, created_at) VALUES (?, ?, ?, '2026-01-01 00:00:00')", conv1ID, "Go chat", specialistID)
	store.Conn.Exec("INSERT INTO conversations (id, title, specialist_id, created_at) VALUES (?, ?, ?, '2026-01-02 00:00:00')", conv2ID, "Go chat 2", specialistID)

	conv, err := repo.FindLatestConversationBySpecialist(specialistID)
	if err != nil || conv == nil {
		t.Fatalf("FindLatestConversationBySpecialist failed: %v %v", err, conv)
	}
	if conv.Title != "Go chat 2" {
		t.Errorf("expected latest conv title 'Go chat 2', got %q", conv.Title)
	}
}

// ─── Models Repository ───────────────────────────────────────────────────────

func TestModelsRepo_UpsertAndGetAllModels(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewModelRepository(store, cs)
	pRepo := db.NewProviderRepository(store, cs)
	err := pRepo.Create(&db.Provider{
		ID: "p-gemini", Name: "Google", Type: "gemini", APIKey: "sk-test-key-12345", IsActive: true,
	})
	if err != nil { t.Fatal(err) }

	model := types.ModelConfig{
		ID:         uuid.NewString(),
		ProviderID: "p-gemini",
		Alias:      "test-gemini",
		Model:      "gemini-2.0-flash",
		Priority:   1,
		IsActive:   true,
	}

	if err := repo.UpsertModel(model); err != nil {
		t.Fatalf("UpsertModel failed: %v", err)
	}

	all, err := repo.GetAllModels()
	if err != nil {
		t.Fatalf("GetAllModels failed: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("expected at least 1 model")
	}

	found := all[0]
	if found.Alias != model.Alias {
		t.Errorf("expected alias %q, got %q", model.Alias, found.Alias)
	}
	// API key is now at provider level, so we check if hydrated correctly
	// but Repo.GetAllModels() Hydrates it from Provider
	if found.APIKey != "sk-test-key-12345" {
		t.Errorf("expected hydrated API key, got %q", found.APIKey)
	}
}

func TestModelsRepo_GetActiveModels_ExcludesInactive(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewModelRepository(store, cs)

	pRepo := db.NewProviderRepository(store, cs)
	_ = pRepo.Create(&db.Provider{ID: "p1", Name: "G", Type: "gemini", APIKey: "key1", IsActive: true})
	_ = pRepo.Create(&db.Provider{ID: "p2", Name: "O", Type: "openai", APIKey: "key2", IsActive: true})

	repo.UpsertModel(types.ModelConfig{
		ID: uuid.NewString(), ProviderID: "p1", Alias: "active",
		Model: "gemini-flash", Priority: 1, IsActive: true,
	})
	repo.UpsertModel(types.ModelConfig{
		ID: uuid.NewString(), ProviderID: "p2", Alias: "inactive",
		Model: "gpt-4o", Priority: 2, IsActive: false,
	})

	active, err := repo.GetActiveModels()
	if err != nil {
		t.Fatalf("GetActiveModels failed: %v", err)
	}
	if len(active) != 1 {
		t.Errorf("expected 1 active model, got %d", len(active))
	}
	if active[0].Alias != "active" {
		t.Errorf("wrong active model returned: %s", active[0].Alias)
	}
}

func TestModelsRepo_DeleteModel(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewModelRepository(store, cs)

	pRepo := db.NewProviderRepository(store, cs)
	_ = pRepo.Create(&db.Provider{ID: "p1", Name: "G", Type: "gemini", APIKey: "key", IsActive: true})

	id := uuid.NewString()
	repo.UpsertModel(types.ModelConfig{
		ID: id, ProviderID: "p1", Alias: "to-delete",
		Model: "gemini-flash", Priority: 1, IsActive: true,
	})

	if err := repo.DeleteModel(id[:8]); err != nil {
		t.Fatalf("DeleteModel failed: %v", err)
	}

	all, _ := repo.GetAllModels()
	if len(all) != 0 {
		t.Errorf("expected 0 models after delete, got %d", len(all))
	}
}

func TestModelsRepo_DeleteModel_NotFound(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewModelRepository(store, cs)

	err := repo.DeleteModel("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent model, got nil")
	}
}

func TestModelsRepo_SetActive(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewModelRepository(store, cs)

	pRepo := db.NewProviderRepository(store, cs)
	_ = pRepo.Create(&db.Provider{ID: "p1", Name: "G", Type: "gemini", APIKey: "key", IsActive: true})

	id := uuid.NewString()
	repo.UpsertModel(types.ModelConfig{
		ID: id, ProviderID: "p1", Alias: "toggle-me",
		Model: "gemini-flash", Priority: 1, IsActive: true,
	})

	if err := repo.SetActive(id[:8], false); err != nil {
		t.Fatalf("SetActive(false) failed: %v", err)
	}

	// Should not be in active models anymore
	active, _ := repo.GetActiveModels()
	if len(active) != 0 {
		t.Errorf("expected 0 active after disabling, got %d", len(active))
	}
}

func TestModelsRepo_GetAllModelsPublic(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewModelRepository(store, cs)

	pRepo := db.NewProviderRepository(store, cs)
	_ = pRepo.Create(&db.Provider{ID: "p1", Name: "O", Type: "openai", APIKey: "sk-secret", IsActive: true})

	repo.UpsertModel(types.ModelConfig{
		ID: uuid.NewString(), ProviderID: "p1", Alias: "pub-model",
		Model: "gpt-4o", Priority: 1, IsActive: true,
	})

	pub, err := repo.GetAllModelsPublic()
	if err != nil {
		t.Fatalf("GetAllModelsPublic failed: %v", err)
	}
	if len(pub) == 0 {
		t.Fatal("expected at least 1 public model")
	}
	// Public accessor should NOT expose API keys
	if pub[0].APIKey != "" {
		t.Errorf("public model should not expose API key, got %q", pub[0].APIKey)
	}
}

func TestModelsRepo_HydrateCapabilities_Gemini(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewModelRepository(store, cs)

	pRepo := db.NewProviderRepository(store, cs)
	_ = pRepo.Create(&db.Provider{ID: "p1", Name: "G", Type: "gemini", APIKey: "key", IsActive: true})

	repo.UpsertModel(types.ModelConfig{
		ID: uuid.NewString(), ProviderID: "p1", Alias: "g2",
		Model: "gemini-2.0-flash", Priority: 1, IsActive: true,
	})

	active, _ := repo.GetActiveModels()
	if len(active) == 0 {
		t.Skip("no active models")
	}
	// Gemini-2.0 should auto-get vision + tools + text
	caps := active[0].Capabilities
	if caps == "" {
		t.Errorf("expected hydrated capabilities, got empty")
	}
	// Gemini should get context_window = 1,000,000
	if active[0].ContextWindow != 1000000 {
		t.Errorf("expected gemini context window 1000000, got %d", active[0].ContextWindow)
	}
}

// ─── Memory Repository ───────────────────────────────────────────────────────

func TestMemoryRepo_SaveAndSearchSimilar(t *testing.T) {
	store := newTestStore(t)
	repo := db.NewMemoryRepository(store)

	// We need a conversation row first for the FK constraint
	chatRepo := db.NewChatRepository(store)
	convID := uuid.NewString()
	chatRepo.CreateConversation(&types.Conversation{ID: convID, Title: "mem test"})

	// Save two embeddings - one semantically "close", one far
	// Save three embeddings: close (score ~0.99), medium (score ~0.7), far (score 0.0)
	queryVec := []float32{1.0, 0.0, 0.0, 0.0}
	closeVec := []float32{0.95, 0.1, 0.0, 0.0}
	medVec := []float32{0.7, 0.7, 0.0, 0.0}
	farVec := []float32{0.0, 0.0, 0.0, 1.0}

	_ = repo.SaveEmbedding(convID, "close text", closeVec)
	_ = repo.SaveEmbedding(convID, "med text", medVec)
	_ = repo.SaveEmbedding(convID, "far text", farVec)

	results, err := repo.SearchSimilar(convID, queryVec, 5)
	if err != nil {
		t.Fatalf("SearchSimilar failed: %v", err)
	}
	// "far text" should be filtered out by > 0.65 threshold
	if len(results) != 2 {
		t.Fatalf("expected 2 results (close and med), got %d", len(results))
	}
	// Sorted descending: close first
	if results[0].Node.TextContent != "close text" {
		t.Errorf("expected 'close text' to rank first, got %q", results[0].Node.TextContent)
	}
	if results[0].Score <= results[1].Score {
		t.Errorf("results should be sorted descending by score: close(%f) <= med(%f)", results[0].Score, results[1].Score)
	}
}

func TestMemoryRepo_SearchSimilar_ReturnsLimit(t *testing.T) {
	store := newTestStore(t)
	repo := db.NewMemoryRepository(store)
	chatRepo := db.NewChatRepository(store)
	convID := uuid.NewString()
	chatRepo.CreateConversation(&types.Conversation{ID: convID, Title: "limit"})

	for i := 0; i < 5; i++ {
		vec := []float32{float32(i) * 0.1, 0.5, 0.3, 0.1}
		repo.SaveEmbedding(convID, "item", vec)
	}

	results, err := repo.SearchSimilar(convID, []float32{0.2, 0.5, 0.3, 0.1}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) > 2 {
		t.Errorf("expected max 2 results with limit=2, got %d", len(results))
	}
}

// ─── Float Encoding ──────────────────────────────────────────────────────────

func TestFloatEncoding_RoundTrip(t *testing.T) {
	original := []float32{0.1, 0.25, 0.5, 0.75, 1.0, -1.0, 3.14}

	encoded, err := db.ConvertFloat32ArrayToBytes(original)
	if err != nil {
		t.Fatalf("ConvertFloat32ArrayToBytes failed: %v", err)
	}

	decoded, err := db.ConvertBytesToFloat32Array(encoded)
	if err != nil {
		t.Fatalf("ConvertBytesToFloat32Array failed: %v", err)
	}

	if len(decoded) != len(original) {
		t.Fatalf("length mismatch: got %d, want %d", len(decoded), len(original))
	}
	for i := range original {
		if decoded[i] != original[i] {
			t.Errorf("mismatch at index %d: got %f, want %f", i, decoded[i], original[i])
		}
	}
}

func TestFloatEncoding_InvalidByteSlice(t *testing.T) {
	_, err := db.ConvertBytesToFloat32Array([]byte{0x01, 0x02, 0x03}) // 3 bytes, not multiple of 4
	if err == nil {
		t.Fatal("expected error for invalid byte slice length")
	}
}

// ─── Memory CosineSimilarity ─────────────────────────────────────────────────

func TestMemoryCosineSimilarity_Identical(t *testing.T) {
	v := []float32{0.5, 0.5, 0.5, 0.5}
	score := db.CosineSimilarity(v, v)
	if score < 0.999 {
		t.Errorf("identical vectors should score ~1.0, got %f", score)
	}
}

func TestMemoryCosineSimilarity_Orthogonal(t *testing.T) {
	a := []float32{1.0, 0.0}
	b := []float32{0.0, 1.0}
	score := db.CosineSimilarity(a, b)
	if score != 0.0 {
		t.Errorf("orthogonal vectors should score 0.0, got %f", score)
	}
}

func TestMemoryCosineSimilarity_LengthMismatch(t *testing.T) {
	a := []float32{1.0, 0.0}
	b := []float32{1.0}
	score := db.CosineSimilarity(a, b)
	if score != 0.0 {
		t.Errorf("length mismatch should produce 0.0, got %f", score)
	}
}
