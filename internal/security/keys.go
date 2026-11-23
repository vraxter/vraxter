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
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vraxter/vraxter/internal/utils"
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
	if err := utils.AtomicWriteFile(privPath, privPEM, 0600); err != nil {
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
	if err := utils.AtomicWriteFile(pubPath, pubPEM, 0600); err != nil {
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

// GetOrCreateEd25519Keys returns the local Ed25519 skill-signing keypair in keysDir,
// generating and atomically saving it (0600 private, 0644 public) if it does not exist.
func (m *KeyManager) GetOrCreateEd25519Keys(signerName string) (*Ed25519KeyPair, error) {
	if err := os.MkdirAll(m.keysDir, 0700); err != nil {
		return nil, err
	}
	privPath := filepath.Join(m.keysDir, "vraxter_ed25519.key")
	if _, err := os.Stat(privPath); err == nil {
		priv, name, err := LoadEd25519PrivateKey(privPath)
		if err != nil {
			return nil, err
		}
		pub := priv.Public().(ed25519.PublicKey)
		if name == "" {
			name = signerName
		}
		return &Ed25519KeyPair{
			KeyID:      ComputeEd25519KeyID(pub),
			SignerName: name,
			PublicKey:  pub,
			PrivateKey: priv,
		}, nil
	}
	return m.GenerateEd25519Keys(signerName)
}

// GenerateEd25519Keys creates a new Ed25519 signing keypair in keysDir.
func (m *KeyManager) GenerateEd25519Keys(signerName string) (*Ed25519KeyPair, error) {
	if err := os.MkdirAll(m.keysDir, 0700); err != nil {
		return nil, err
	}
	kp, err := GenerateEd25519KeyPair(signerName)
	if err != nil {
		return nil, err
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(kp.PrivateKey)
	if err != nil {
		return nil, err
	}
	privPEM := pem.EncodeToMemory(&pem.Block{
		Type:    "PRIVATE KEY",
		Headers: map[string]string{"Key-ID": kp.KeyID, "Signer-Name": kp.SignerName},
		Bytes:   privDER,
	})
	if err := utils.AtomicWriteFile(filepath.Join(m.keysDir, "vraxter_ed25519.key"), privPEM, 0600); err != nil {
		return nil, err
	}

	pubDER, err := x509.MarshalPKIXPublicKey(kp.PublicKey)
	if err != nil {
		return nil, err
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{
		Type:    "PUBLIC KEY",
		Headers: map[string]string{"Key-ID": kp.KeyID, "Signer-Name": kp.SignerName},
		Bytes:   pubDER,
	})
	if err := utils.AtomicWriteFile(filepath.Join(m.keysDir, "vraxter_ed25519.pub"), pubPEM, 0644); err != nil {
		return nil, err
	}
	return kp, nil
}
