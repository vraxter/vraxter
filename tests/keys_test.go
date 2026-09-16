// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vraxter/vraxter/internal/security"
)

func TestKeyManager_GenerateAndLoad(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vraxter-keys-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	km := security.NewKeyManager(tmpDir)

	// 1. Generate keys
	privKey1, err := km.GetOrCreateKeys()
	if err != nil {
		t.Fatalf("failed to generate keys: %v", err)
	}
	if privKey1 == nil {
		t.Fatalf("returned private key is nil")
	}

	// 2. Load keys (should not generate new ones)
	privKey2, err := km.GetOrCreateKeys()
	if err != nil {
		t.Fatalf("failed to load keys: %v", err)
	}

	if privKey1.D.Cmp(privKey2.D) != 0 {
		t.Fatalf("loaded key does not match generated key")
	}

	// 3. Load public key
	pubKey, err := km.LoadPublicKey()
	if err != nil {
		t.Fatalf("failed to load public key: %v", err)
	}
	if pubKey == nil {
		t.Fatalf("returned public key is nil")
	}

	// 4. Verify file existence
	if _, err := os.Stat(filepath.Join(tmpDir, "vraxter_id")); os.IsNotExist(err) {
		t.Fatalf("private key file does not exist")
	}
	if _, err := os.Stat(filepath.Join(tmpDir, "vraxter_id.pub")); os.IsNotExist(err) {
		t.Fatalf("public key file does not exist")
	}
}

func TestKeyManager_MissingDirectory(t *testing.T) {
	tmpDir := "/tmp/vraxter/missing/directory/keys"
	_ = os.RemoveAll(tmpDir) // Ensure it's gone

	km := security.NewKeyManager(tmpDir)
	_, err := km.GetOrCreateKeys()
	if err != nil {
		t.Fatalf("expected KeyManager to automatically create directories, but failed: %v", err)
	}
	
	_ = os.RemoveAll("/tmp/vraxter/missing")
}
