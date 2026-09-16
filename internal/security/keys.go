// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
)

// KeyManager handles the local identity of Vraxter for skill signing
type KeyManager struct {
	keysDir string
}

func NewKeyManager(keysDir string) *KeyManager {
	return &KeyManager{keysDir: keysDir}
}

// GetOrCreateKeys returns the local ECDSA private key, generating it if it doesn't exist
func (m *KeyManager) GetOrCreateKeys() (*ecdsa.PrivateKey, error) {
	os.MkdirAll(m.keysDir, 0700)
	privPath := filepath.Join(m.keysDir, "vraxter_id")

	if _, err := os.Stat(privPath); err == nil {
		return m.LoadPrivateKey()
	}

	return m.GenerateKeys()
}

// GenerateKeys creates a new P-256 key pair and saves them to disk
func (m *KeyManager) GenerateKeys() (*ecdsa.PrivateKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	// Save Private Key
	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return nil, err
	}

	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: privBytes,
	})

	privPath := filepath.Join(m.keysDir, "vraxter_id")
	if err := os.WriteFile(privPath, privPEM, 0600); err != nil {
		return nil, err
	}

	// Save Public Key
	pubBytes, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, err
	}

	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubBytes,
	})

	pubPath := filepath.Join(m.keysDir, "vraxter_id.pub")
	if err := os.WriteFile(pubPath, pubPEM, 0644); err != nil {
		return nil, err
	}

	return priv, nil
}

// LoadPrivateKey reads the private key from disk
func (m *KeyManager) LoadPrivateKey() (*ecdsa.PrivateKey, error) {
	privPath := filepath.Join(m.keysDir, "vraxter_id")
	data, err := os.ReadFile(privPath)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode private key PEM")
	}

	return x509.ParseECPrivateKey(block.Bytes)
}

// LoadPublicKey reads the public key from disk
func (m *KeyManager) LoadPublicKey() (*ecdsa.PublicKey, error) {
	pubPath := filepath.Join(m.keysDir, "vraxter_id.pub")
	data, err := os.ReadFile(pubPath)
	if err != nil {
		return nil, err
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("failed to decode public key PEM")
	}

	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}

	return pub.(*ecdsa.PublicKey), nil
}
