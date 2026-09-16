// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package security

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// GetOrCreateDaemonKey retrieves the existing daemon API key or creates a new one with strict 0600 permissions.
func GetOrCreateDaemonKey(appDir string) (string, error) {
	keyPath := filepath.Join(appDir, "daemon.key")

	// Check if the key already exists
	if data, err := os.ReadFile(keyPath); err == nil {
		key := strings.TrimSpace(string(data))
		if len(key) >= 32 {
			return key, nil
		}
	}

	// Generate a new 32-byte secure hex string (64 characters)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes for daemon key: %w", err)
	}
	key := "vrx_" + hex.EncodeToString(bytes)

	// Save to disk with 0600 permissions (read/write for owner only)
	if err := os.WriteFile(keyPath, []byte(key), 0600); err != nil {
		return "", fmt.Errorf("failed to write daemon key to %s: %w", keyPath, err)
	}

	return key, nil
}

// ValidateAPIKey performs a constant-time comparison of the provided API key against the expected one.
func ValidateAPIKey(expected, provided string) error {
	// Strip the Bearer prefix if it exists
	provided = strings.TrimPrefix(provided, "Bearer ")
	provided = strings.TrimSpace(provided)

	if subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1 {
		return nil
	}
	return fmt.Errorf("invalid or missing API key")
}
