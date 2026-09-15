// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package utils

import (
	"crypto/sha256"
	"fmt"
	"time"
)

// GenerateShortID produces a stable 8-character hex identifier based on a string seed and timestamp.
// This is used for Providers and Models to ensure easy CLI/TUI reference without long UUIDs.
func GenerateShortID(seed string) string {
	ts := time.Now().UnixNano()
	payload := fmt.Sprintf("%s-%d", seed, ts)
	hash := sha256.Sum256([]byte(payload))
	return fmt.Sprintf("%x", hash)[:8]
}

// IsShortID checks if a string matches the 8-character hex pattern.
func IsShortID(token string) bool {
	if len(token) != 8 {
		return false
	}
	for _, r := range token {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
