// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// The size of AES-256 keys in bytes
const keySize = 32

// CryptoService handles encryption/decryption of secrets (API Keys) at rest
type CryptoService struct {
	masterKey []byte
}

// NewCryptoService ensures a master key exists in the specified path, creating it if necessary
func NewCryptoService(keyPath string) (*CryptoService, error) {
	var masterKey []byte

	// 1. Check if the keyfile exists
	if _, err := os.Stat(keyPath); os.IsNotExist(err) {
		// Generate new random 32-byte AES key
		masterKey = make([]byte, keySize)
		if _, err := io.ReadFull(rand.Reader, masterKey); err != nil {
			return nil, fmt.Errorf("failed generating random crypto key: %w", err)
		}

		// Save it strictly with 0600 permissions
		if err := os.WriteFile(keyPath, masterKey, 0600); err != nil {
			return nil, fmt.Errorf("failed to save .masterkey securely: %w", err)
		}
	} else {
		// Read existing key
		masterKey, err = os.ReadFile(keyPath)
		if err != nil {
			return nil, fmt.Errorf("could not read existing .masterkey: %w", err)
		}
		if len(masterKey) != keySize {
			return nil, fmt.Errorf("invalid .masterkey format: length is not 32 bytes")
		}
	}

	return &CryptoService{masterKey: masterKey}, nil
}

// Encrypt encrypts a plaintext string into a hex-encoded AES-GCM ciphertext
func (s *CryptoService) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil // Nothing to encrypt
	}

	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := aesGCM.Seal(nonce, nonce, []byte(plaintext), nil)
	return hex.EncodeToString(ciphertext), nil
}

// Decrypt turns a hex-encoded AES-GCM ciphertext back into the original string
func (s *CryptoService) Decrypt(hexCiphertext string) (string, error) {
	if hexCiphertext == "" {
		return "", nil // Nothing to decrypt
	}

	data, err := hex.DecodeString(hexCiphertext)
	if err != nil {
		return "", err // Not a hex string, might be old plaintext DB data?
	}

	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return "", err
	}

	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := aesGCM.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := aesGCM.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt api key (wrong masterkey?): %w", err)
	}

	return string(plaintext), nil
}
