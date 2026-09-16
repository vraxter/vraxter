// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/vraxter/vraxter/internal/db"
	"google.golang.org/grpc/metadata"
)

func TestEnforceScope_MasterKey(t *testing.T) {
	// Create context with wildcard scope
	ctx := context.WithValue(context.Background(), scopesCtxKey, []string{"*"})
	
	err := EnforceScope(ctx, "skills:write")
	if err != nil {
		t.Fatalf("Expected master key wildcard to pass, got: %v", err)
	}
}

func TestEnforceScope_ValidToken(t *testing.T) {
	ctx := context.WithValue(context.Background(), scopesCtxKey, []string{"skills:read", "chat:write"})
	
	err := EnforceScope(ctx, "skills:read")
	if err != nil {
		t.Fatalf("Expected exact scope to pass, got: %v", err)
	}
}

func TestEnforceScope_InvalidScope(t *testing.T) {
	ctx := context.WithValue(context.Background(), scopesCtxKey, []string{"skills:read"})
	
	err := EnforceScope(ctx, "skills:write")
	if err == nil {
		t.Fatal("Expected missing scope to fail, but it passed")
	}
}

func TestEnforceScope_NoScopesFound(t *testing.T) {
	ctx := context.Background()
	
	err := EnforceScope(ctx, "skills:write")
	if err == nil {
		t.Fatal("Expected empty context to fail, but it passed")
	}
}

func TestAuthorize_MasterKey(t *testing.T) {
	expectedKey := "master-secret-key"
	md := metadata.New(map[string]string{"authorization": expectedKey})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	newCtx, err := authorize(ctx, expectedKey)
	if err != nil {
		t.Fatalf("authorize failed for master key: %v", err)
	}

	scopes, ok := newCtx.Value(scopesCtxKey).([]string)
	if !ok || len(scopes) != 1 || scopes[0] != "*" {
		t.Fatalf("Expected wildcard scope, got %v", scopes)
	}
}

func TestAuthorize_ScopedToken(t *testing.T) {
	// Setup in-memory DB
	database, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("Failed to initialize test DB: %v", err)
	}
	defer database.Close()
	
	// Create table manually since we might bypass full init
	database.Conn.Exec(`CREATE TABLE IF NOT EXISTS api_keys (id TEXT PRIMARY KEY, name TEXT, key_hash TEXT, scopes TEXT, created_at TEXT)`)

	repo := db.NewAPIKeyRepository(database)
	SetAPIKeyRepo(repo)

	// Create a token
	tokenStr := "test-token-123"
	hash := sha256.Sum256([]byte(tokenStr))
	hashStr := hex.EncodeToString(hash[:])
	
	err = repo.Create(&db.APIKey{
		ID:        "test-id",
		KeyHash:   hashStr,
		Name:      "Test Client",
		Scopes:    "chat:write,skills:read",
		CreatedAt: "2026-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("Failed to create key: %v", err)
	}

	md := metadata.New(map[string]string{"authorization": "Bearer " + tokenStr})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	newCtx, err := authorize(ctx, "master-secret-key")
	if err != nil {
		t.Fatalf("authorize failed for valid token: %v", err)
	}

	scopes, ok := newCtx.Value(scopesCtxKey).([]string)
	if !ok || len(scopes) != 2 || scopes[0] != "chat:write" || scopes[1] != "skills:read" {
		t.Fatalf("Expected valid scopes, got %v", scopes)
	}
}

func TestAuthorize_InvalidToken(t *testing.T) {
	database, _ := db.NewStore(":memory:")
	repo := db.NewAPIKeyRepository(database)
	SetAPIKeyRepo(repo)

	md := metadata.New(map[string]string{"authorization": "Bearer invalid-token"})
	ctx := metadata.NewIncomingContext(context.Background(), md)

	_, err := authorize(ctx, "master-secret-key")
	if err == nil {
		t.Fatal("Expected invalid token to fail, but it passed")
	}
}
