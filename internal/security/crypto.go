// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package security

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vraxter/vraxter/internal/utils"
)

// The size of AES-256 keys in bytes
const keySize = 32

const (
	// EncryptedValuePrefix marks column values encrypted with AES-256-GCM at rest.
	EncryptedValuePrefix = "vraxter-enc-v1:"
	// EncryptedBlobMagic marks binary blobs and sealed SQLite files encrypted with AES-256-GCM.
	EncryptedBlobMagic = "VRXENC01"
	// DefaultDBMasterKeyFilename is the filename of the 32-byte AES-256 data master key.
	DefaultDBMasterKeyFilename = "db_master.key"
)

// CryptoService handles encryption/decryption of secrets and SQLite data at rest using AES-256-GCM.
type CryptoService struct {
	masterKey []byte
}

// NewDBMasterCryptoService loads or atomically creates <appDir>/db_master.key (32-byte CSPRNG, mode 0600).
func NewDBMasterCryptoService(appDir string) (*CryptoService, error) {
	if strings.TrimSpace(appDir) == "" {
		key := make([]byte, keySize)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, err
		}
		return &CryptoService{masterKey: key}, nil
	}
	if err := os.MkdirAll(appDir, 0700); err != nil {
		return nil, fmt.Errorf("failed creating app directory for db_master.key: %w", err)
	}
	return NewCryptoService(filepath.Join(appDir, DefaultDBMasterKeyFilename))
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

		// Save it atomically with 0600 permissions
		if err := utils.AtomicWriteFile(keyPath, masterKey, 0600); err != nil {
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
		_ = os.Chmod(keyPath, 0600)
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
	if strings.HasPrefix(hexCiphertext, EncryptedValuePrefix) {
		hexCiphertext = strings.TrimPrefix(hexCiphertext, EncryptedValuePrefix)
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

// EncryptEnvelope encrypts plaintext with AES-256-GCM (96-bit random nonce) and prefixes it with "vraxter-enc-v1:".
// Empty strings or already-encrypted envelopes are returned unchanged.
func (s *CryptoService) EncryptEnvelope(plaintext string) (string, error) {
	if s == nil || plaintext == "" || strings.HasPrefix(plaintext, EncryptedValuePrefix) {
		return plaintext, nil
	}
	encHex, err := s.Encrypt(plaintext)
	if err != nil {
		return "", err
	}
	return EncryptedValuePrefix + encHex, nil
}

// DecryptEnvelope decrypts a "vraxter-enc-v1:" envelope back to plaintext.
// Values without the prefix are treated as legacy plaintext for backward compatibility.
func (s *CryptoService) DecryptEnvelope(value string) (string, error) {
	if s == nil || value == "" || !strings.HasPrefix(value, EncryptedValuePrefix) {
		return value, nil
	}
	hexBody := strings.TrimPrefix(value, EncryptedValuePrefix)
	return s.Decrypt(hexBody)
}

// EncryptBytes encrypts arbitrary binary data with AES-256-GCM and prepends EncryptedBlobMagic ("VRXENC01").
func (s *CryptoService) EncryptBytes(plain []byte) ([]byte, error) {
	if s == nil || len(plain) == 0 {
		return plain, nil
	}
	if bytes.HasPrefix(plain, []byte(EncryptedBlobMagic)) {
		return plain, nil
	}

	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonce := make([]byte, aesGCM.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}

	sealed := aesGCM.Seal(nil, nonce, plain, []byte(EncryptedBlobMagic))
	out := make([]byte, 0, len(EncryptedBlobMagic)+len(nonce)+len(sealed))
	out = append(out, []byte(EncryptedBlobMagic)...)
	out = append(out, nonce...)
	out = append(out, sealed...)
	return out, nil
}

// DecryptBytes decrypts a binary blob produced by EncryptBytes.
// If data does not start with EncryptedBlobMagic, it is returned as-is for legacy blob compatibility.
func (s *CryptoService) DecryptBytes(data []byte) ([]byte, error) {
	if s == nil || len(data) == 0 {
		return data, nil
	}
	magic := []byte(EncryptedBlobMagic)
	if !bytes.HasPrefix(data, magic) {
		return data, nil
	}

	block, err := aes.NewCipher(s.masterKey)
	if err != nil {
		return nil, err
	}
	aesGCM, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	nonceSize := aesGCM.NonceSize()
	payload := data[len(magic):]
	if len(payload) < nonceSize {
		return nil, fmt.Errorf("encrypted blob too short")
	}

	nonce := payload[:nonceSize]
	ciphertext := payload[nonceSize:]
	plain, err := aesGCM.Open(nil, nonce, ciphertext, magic)
	if err != nil {
		return nil, fmt.Errorf("AES-256-GCM blob authentication failed: %w", err)
	}
	return plain, nil
}

// EncryptFile reads srcPath, encrypts its entire contents with AES-256-GCM (VRXENC01 envelope),
// and writes the result atomically to destEncPath with 0600 permissions.
func (s *CryptoService) EncryptFile(srcPath, destEncPath string) error {
	if s == nil {
		return fmt.Errorf("nil CryptoService")
	}
	raw, err := os.ReadFile(srcPath)
	if err != nil {
		return fmt.Errorf("failed reading source file for encryption: %w", err)
	}
	enc, err := s.EncryptBytes(raw)
	if err != nil {
		return err
	}
	return utils.AtomicWriteFile(destEncPath, enc, 0600)
}

// DecryptFile reads an AES-256-GCM encrypted file (VRXENC01 envelope) from srcEncPath
// and writes the decrypted plaintext atomically to destPath with 0600 permissions.
func (s *CryptoService) DecryptFile(srcEncPath, destPath string) error {
	if s == nil {
		return fmt.Errorf("nil CryptoService")
	}
	enc, err := os.ReadFile(srcEncPath)
	if err != nil {
		return fmt.Errorf("failed reading encrypted file %s: %w", srcEncPath, err)
	}
	if !bytes.HasPrefix(enc, []byte(EncryptedBlobMagic)) {
		return fmt.Errorf("file %s is not a valid VRXENC01 encrypted archive", srcEncPath)
	}
	plain, err := s.DecryptBytes(enc)
	if err != nil {
		return err
	}
	return utils.AtomicWriteFile(destPath, plain, 0600)
}
