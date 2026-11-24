// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package db

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vraxter/vraxter/pkg/types"
)

// TestHelperProcess_OpenLockedDB is invoked as a child OS process by TestStore_ExclusiveLockProtectsWorkingDB
// to verify cross-process OS locking on vraxter.db.lock.
func TestHelperProcess_OpenLockedDB(t *testing.T) {
	dbPath := os.Getenv("VRAXTER_TEST_LOCK_DB_PATH")
	if dbPath == "" {
		return
	}
	store, err := NewStoreWithOptions(dbPath, StoreOptions{EncryptAtRest: true})
	if err == nil {
		_ = store.Close()
		t.Fatal("child process unexpectedly opened an exclusively locked database")
	}
	if !errors.Is(err, ErrDatabaseLocked) {
		t.Fatalf("expected ErrDatabaseLocked in child process, got: %v", err)
	}
}

func TestStore_ExclusiveLockProtectsWorkingDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vraxter.db")

	store1, err := NewStoreWithOptions(dbPath, StoreOptions{EncryptAtRest: true})
	if err != nil {
		t.Fatalf("failed opening primary sealed store: %v", err)
	}

	// Verify lockfile and active working DB exist while store1 is open.
	lockPath := dbPath + ".lock"
	if _, err := os.Stat(lockPath); err != nil {
		store1.Close()
		t.Fatalf("expected exclusive lockfile %s to exist: %v", lockPath, err)
	}
	workingPath := filepath.Join(dir, ".vraxter.db.working")
	if _, err := os.Stat(workingPath); err != nil {
		store1.Close()
		t.Fatalf("expected working DB %s to exist while sealed store is open: %v", workingPath, err)
	}

	chatRepo := NewChatRepository(store1)
	if err := chatRepo.CreateConversation(&types.Conversation{ID: "conv-lock-1", Title: "Mission Alpha"}); err != nil {
		store1.Close()
		t.Fatalf("CreateConversation failed: %v", err)
	}
	if err := chatRepo.SaveMessage(&types.Message{
		ID:             "msg-lock-1",
		ConversationID: "conv-lock-1",
		Role:           "user",
		Content:        "CONFIDENTIAL_FLIGHT_DATA",
		Timestamp:      time.Now(),
	}); err != nil {
		store1.Close()
		t.Fatalf("SaveMessage failed: %v", err)
	}

	// 1. In-process concurrent open must fail closed with ErrDatabaseLocked without touching .working.
	if store2, err := NewStoreWithOptions(dbPath, StoreOptions{EncryptAtRest: true}); err == nil {
		_ = store2.Close()
		_ = store1.Close()
		t.Fatal("expected second in-process NewStoreWithOptions to fail with ErrDatabaseLocked")
	} else if !errors.Is(err, ErrDatabaseLocked) {
		_ = store1.Close()
		t.Fatalf("expected ErrDatabaseLocked, got: %v", err)
	}

	// 2. Cross-process concurrent open (separate OS process) must also fail closed without removing .working.
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess_OpenLockedDB$")
	cmd.Env = append(os.Environ(), "VRAXTER_TEST_LOCK_DB_PATH="+dbPath)
	if out, err := cmd.CombinedOutput(); err != nil {
		_ = store1.Close()
		t.Fatalf("cross-process lock verification failed: %v\noutput:\n%s", err, string(out))
	}

	// Confirm .working still exists and store1's live connection can still read & write intact.
	if _, err := os.Stat(workingPath); err != nil {
		_ = store1.Close()
		t.Fatalf("working DB was deleted despite exclusive lock: %v", err)
	}
	msgs, err := chatRepo.GetFullConversation("conv-lock-1")
	if err != nil || len(msgs) != 1 || msgs[0].Content != "CONFIDENTIAL_FLIGHT_DATA" {
		_ = store1.Close()
		t.Fatalf("live store corrupted after blocked concurrent opens: msgs=%+v err=%v", msgs, err)
	}

	// 3. Closing store1 seals .db.enc and releases the lock so a subsequent open succeeds cleanly.
	if err := store1.Close(); err != nil {
		t.Fatalf("store1.Close failed: %v", err)
	}
	if _, err := os.Stat(dbPath + ".enc"); err != nil {
		t.Fatalf("expected sealed %s.enc after close: %v", dbPath, err)
	}

	store3, err := NewStoreWithOptions(dbPath, StoreOptions{EncryptAtRest: true})
	if err != nil {
		t.Fatalf("expected reopening after lock release to succeed, got: %v", err)
	}
	defer store3.Close()

	msgsAfter, err := NewChatRepository(store3).GetFullConversation("conv-lock-1")
	if err != nil || len(msgsAfter) != 1 || msgsAfter[0].Content != "CONFIDENTIAL_FLIGHT_DATA" {
		t.Fatalf("expected sealed data intact after reopen, got msgs=%+v err=%v", msgsAfter, err)
	}
}

func TestStore_EncryptFieldAndBlobFailClosed(t *testing.T) {
	store, err := NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	// Disable the cryptographic service to simulate key unavailability / corruption.
	store.SetCrypto(nil)

	if _, err := store.EncryptField("TOP_SECRET_PAYLOAD"); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("expected ErrEncryptionUnavailable from EncryptField, got: %v", err)
	}
	if _, err := store.EncryptBlob([]byte("TOP_SECRET_BYTES")); !errors.Is(err, ErrEncryptionUnavailable) {
		t.Fatalf("expected ErrEncryptionUnavailable from EncryptBlob, got: %v", err)
	}

	// Every repository write must abort with an error and persist ZERO plaintext rows.
	chatRepo := NewChatRepository(store)
	if err := chatRepo.CreateConversation(&types.Conversation{ID: "conv-fail-1", Title: "Secret Title"}); err == nil {
		t.Fatal("expected CreateConversation to fail closed when encryption is unavailable")
	}

	memRepo := NewMemoryRepository(store)
	if err := memRepo.SaveEmbedding("conv-fail-1", "Secret RAG text", []float32{0.1, 0.2, 0.3}); err == nil {
		t.Fatal("expected SaveEmbedding to fail closed when encryption is unavailable")
	}

	specRepo := NewSpecialistRepository(store)
	if err := specRepo.CreateSpecialist(types.Specialist{
		ID:           "spec-fail-1",
		Name:         "Auditor",
		Expertise:    "Classified Security",
		SystemPrompt: "Secret prompt",
	}); err == nil {
		t.Fatal("expected CreateSpecialist to fail closed when encryption is unavailable")
	}

	skillRepo := NewSkillRepository(store)
	if err := skillRepo.UpsertSkill(types.SkillManifest{
		ID:           "skill-fail-1",
		Name:         "Secret Skill",
		Version:      "1.0.0",
		Command:      "secret.wasm",
		Language:     "go",
		Engine:       "wasm",
		Checksum:     strings.Repeat("a", 64),
		ParamsSchema: `{"type":"object"}`,
	}); err == nil {
		t.Fatal("expected UpsertSkill to fail closed when encryption is unavailable")
	}

	var convCount, specCount, skillCount, embCount int
	_ = store.Conn.QueryRow("SELECT COUNT(*) FROM conversations").Scan(&convCount)
	_ = store.Conn.QueryRow("SELECT COUNT(*) FROM specialists").Scan(&specCount)
	_ = store.Conn.QueryRow("SELECT COUNT(*) FROM skills").Scan(&skillCount)
	_ = store.Conn.QueryRow("SELECT COUNT(*) FROM embeddings").Scan(&embCount)
	if convCount+specCount+skillCount+embCount != 0 {
		t.Fatalf("fail-closed violation: plaintext rows persisted despite encryption failure (conv=%d spec=%d skill=%d emb=%d)",
			convCount, specCount, skillCount, embCount)
	}
}
