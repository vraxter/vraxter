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

	"github.com/vraxter/vraxter/internal/security"
)

func TestKeyManager_GenerateKeys_CreatesFiles(t *testing.T) {
	dir := t.TempDir()
	km := security.NewKeyManager(dir)

	priv, err := km.GenerateKeys()
	if err != nil {
		t.Fatalf("GenerateKeys failed: %v", err)
	}
	if priv == nil {
		t.Fatal("expected non-nil private key")
	}
}

func TestKeyManager_LoadPrivateKey_AfterGenerate(t *testing.T) {
	dir := t.TempDir()
	km := security.NewKeyManager(dir)

	if _, err := km.GenerateKeys(); err != nil {
		t.Fatalf("GenerateKeys failed: %v", err)
	}

	loaded, err := km.LoadPrivateKey()
	if err != nil {
		t.Fatalf("LoadPrivateKey failed: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected non-nil loaded private key")
	}
}

func TestKeyManager_LoadPublicKey_AfterGenerate(t *testing.T) {
	dir := t.TempDir()
	km := security.NewKeyManager(dir)

	if _, err := km.GenerateKeys(); err != nil {
		t.Fatalf("GenerateKeys failed: %v", err)
	}

	pub, err := km.LoadPublicKey()
	if err != nil {
		t.Fatalf("LoadPublicKey failed: %v", err)
	}
	if pub == nil {
		t.Fatal("expected non-nil public key")
	}
}

func TestKeyManager_GetOrCreateKeys_CreatesOnFirstCall(t *testing.T) {
	dir := t.TempDir()
	km := security.NewKeyManager(dir)

	priv, err := km.GetOrCreateKeys()
	if err != nil {
		t.Fatalf("GetOrCreateKeys failed: %v", err)
	}
	if priv == nil {
		t.Fatal("expected non-nil key on first call")
	}
}

func TestKeyManager_GetOrCreateKeys_LoadsOnSecondCall(t *testing.T) {
	dir := t.TempDir()
	km := security.NewKeyManager(dir)

	priv1, _ := km.GetOrCreateKeys()
	priv2, err := km.GetOrCreateKeys()
	if err != nil {
		t.Fatalf("second GetOrCreateKeys failed: %v", err)
	}

	// Both calls should return a valid key for the same directory
	if priv1 == nil || priv2 == nil {
		t.Fatal("expected non-nil keys on both calls")
	}
}

func TestKeyManager_LoadPrivateKey_NoFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	km := security.NewKeyManager(dir)

	_, err := km.LoadPrivateKey()
	if err == nil {
		t.Fatal("expected error loading private key from empty dir")
	}
}

func TestKeyManager_LoadPublicKey_NoFile_ReturnsError(t *testing.T) {
	dir := t.TempDir()
	km := security.NewKeyManager(dir)

	_, err := km.LoadPublicKey()
	if err == nil {
		t.Fatal("expected error loading public key from empty dir")
	}
}

func TestCryptoService_EncryptDecryptRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cs, err := security.NewCryptoService(dir + "/.key")
	if err != nil {
		t.Fatalf("NewCryptoService failed: %v", err)
	}

	plaintext := "sk-test-api-key-abc123"
	encrypted, err := cs.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if encrypted == plaintext {
		t.Error("encrypted value should differ from plaintext")
	}

	decrypted, err := cs.Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if decrypted != plaintext {
		t.Errorf("round-trip failed: expected %q, got %q", plaintext, decrypted)
	}
}

func TestCryptoService_Encrypt_EmptyString(t *testing.T) {
	dir := t.TempDir()
	cs, _ := security.NewCryptoService(dir + "/.key")

	encrypted, err := cs.Encrypt("")
	if err != nil {
		t.Fatalf("Encrypt empty string failed: %v", err)
	}
	// Empty string should encrypt to empty (per implementation)
	if encrypted != "" {
		t.Errorf("expected empty encrypted result for empty input, got %q", encrypted)
	}
}

func TestCryptoService_Decrypt_EmptyString(t *testing.T) {
	dir := t.TempDir()
	cs, _ := security.NewCryptoService(dir + "/.key")

	decrypted, err := cs.Decrypt("")
	if err != nil {
		t.Fatalf("Decrypt empty string failed: %v", err)
	}
	if decrypted != "" {
		t.Errorf("expected empty result for empty ciphertext, got %q", decrypted)
	}
}

func TestCryptoService_Decrypt_InvalidHex(t *testing.T) {
	dir := t.TempDir()
	cs, _ := security.NewCryptoService(dir + "/.key")

	_, err := cs.Decrypt("not-valid-hex!!")
	if err == nil {
		t.Fatal("expected error for non-hex ciphertext")
	}
}

func TestCryptoService_Decrypt_TooShort(t *testing.T) {
	dir := t.TempDir()
	cs, _ := security.NewCryptoService(dir + "/.key")

	// Valid hex but too short to contain nonce
	_, err := cs.Decrypt("deadbeef")
	if err == nil {
		t.Fatal("expected error for too-short ciphertext")
	}
}

func TestCryptoService_LoadsExistingKey(t *testing.T) {
	dir := t.TempDir()
	keyPath := dir + "/.masterkey"

	// Create first instance (generates key)
	cs1, err := security.NewCryptoService(keyPath)
	if err != nil {
		t.Fatalf("first NewCryptoService failed: %v", err)
	}

	enc, _ := cs1.Encrypt("hello")

	// Create second instance (loads existing key)
	cs2, err := security.NewCryptoService(keyPath)
	if err != nil {
		t.Fatalf("second NewCryptoService failed: %v", err)
	}

	dec, err := cs2.Decrypt(enc)
	if err != nil {
		t.Fatalf("Decrypt with reloaded key failed: %v", err)
	}
	if dec != "hello" {
		t.Errorf("expected 'hello', got %q", dec)
	}
}
