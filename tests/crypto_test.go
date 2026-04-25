package tests

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/patagonicrune/vraxter/internal/security"
)

func TestCryptoService_EncryptDecrypt(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "vraxter-crypto-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	keyPath := filepath.Join(tmpDir, "master.key")

	// 1. Initialize Crypto Service (should generate new keys automatically for testing if missing)
	service, err := security.NewCryptoService(keyPath)
	if err != nil {
		t.Fatalf("failed to initialize CryptoService: %v", err)
	}

	plaintext := "sk-abc123secretkey456"

	// 2. Encrypt
	cipherHex, err := service.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("failed to encrypt: %v", err)
	}
	if cipherHex == "" || cipherHex == plaintext {
		t.Fatalf("encryption failed, cipher output invalid: %s", cipherHex)
	}

	// 3. Decrypt
	decrypted, err := service.Decrypt(cipherHex)
	if err != nil {
		t.Fatalf("failed to decrypt: %v", err)
	}

	if decrypted != plaintext {
		t.Fatalf("expected plaintext '%s', got '%s'", plaintext, decrypted)
	}
}

func TestCryptoService_InvalidDecryption(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "vraxter-crypto-test-*")
	defer os.RemoveAll(tmpDir)

	service, _ := security.NewCryptoService(filepath.Join(tmpDir, "master.key"))

	_, err := service.Decrypt("invalid-hex-noise")
	if err == nil {
		t.Fatalf("Expected decryption to fail for invalid hex, but it succeeded")
	}
}
