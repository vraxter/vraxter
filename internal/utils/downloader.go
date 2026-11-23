// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package utils

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultDownloadTimeout bounds full SDK/artifact downloads on DDIL links while
// NewDDILHTTPClient enforces 10s connect/TLS and 30s response header timeouts.
const DefaultDownloadTimeout = 10 * time.Minute

// NormalizeAndValidateSHA256 validates that a string is a 64-character lowercase hex SHA-256 digest.
func NormalizeAndValidateSHA256(expectedSHA256 string) (string, error) {
	cleaned := strings.ToLower(strings.TrimSpace(expectedSHA256))
	if len(cleaned) != 64 {
		return "", fmt.Errorf("VRAXTER SHIELD: missing or invalid expected SHA-256 checksum (must be 64 hex characters)")
	}
	decoded, err := hex.DecodeString(cleaned)
	if err != nil || len(decoded) != sha256.Size {
		return "", fmt.Errorf("VRAXTER SHIELD: malformed SHA-256 checksum %q", expectedSHA256)
	}
	return cleaned, nil
}

// DownloadFile downloads a file from rawURL using the DDIL-hardened HTTP client
// (respecting the active AirgapPerimeter) and writes it atomically to destPath.
func DownloadFile(destPath string, rawURL string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultDownloadTimeout)
	defer cancel()
	return DownloadFileWithContext(ctx, destPath, rawURL)
}

// DownloadFileWithContext downloads rawURL to destPath with context cancellation,
// DDIL transport timeouts, and atomic temp-file replacement so failed downloads
// never leave partial or corrupt artifacts at destPath.
func DownloadFileWithContext(ctx context.Context, destPath string, rawURL string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("invalid download request: %w", err)
	}

	client := NewDDILHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	return AtomicWriteFromReader(destPath, resp.Body, 0, 0600)
}

// DownloadFileVerified downloads rawURL to a temporary file while computing its SHA-256
// in streaming, verifies the digest against expectedSHA256 before moving into destPath,
// and deletes the temporary file on any mismatch or error (Fail-Closed).
func DownloadFileVerified(destPath string, rawURL string, expectedSHA256 string) error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultDownloadTimeout)
	defer cancel()
	return DownloadFileVerifiedWithContext(ctx, destPath, rawURL, expectedSHA256)
}

// DownloadFileVerifiedWithContext downloads rawURL with context cancellation and mandatory
// streaming SHA-256 verification before atomically moving the file to destPath.
func DownloadFileVerifiedWithContext(ctx context.Context, destPath string, rawURL string, expectedSHA256 string) error {
	expectedHex, err := NormalizeAndValidateSHA256(expectedSHA256)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("invalid download request: %w", err)
	}

	client := NewDDILHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("failed to ensure target directory %s: %w", destDir, err)
	}

	tmpFile, err := os.CreateTemp(destDir, ".tmp-dl-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary download file in %s: %w", destDir, err)
	}
	tmpPath := tmpFile.Name()
	closed := false
	cleanup := true
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	hasher := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmpFile, hasher), resp.Body); err != nil {
		return fmt.Errorf("failed streaming download for %s: %w", destPath, err)
	}

	actualHex := hex.EncodeToString(hasher.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(actualHex), []byte(expectedHex)) != 1 {
		return fmt.Errorf("VRAXTER SHIELD: SHA-256 checksum mismatch for %s: expected %s, got %s", filepath.Base(destPath), expectedHex, actualHex)
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed syncing verified download for %s: %w", destPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		closed = true
		return fmt.Errorf("failed closing verified download for %s: %w", destPath, err)
	}
	closed = true

	if err := os.Chmod(tmpPath, 0600); err != nil {
		return fmt.Errorf("failed setting permissions on verified download for %s: %w", destPath, err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed atomically placing verified download at %s: %w", destPath, err)
	}
	cleanup = false

	_ = os.Chmod(destPath, 0600)
	syncParentDir(destDir)
	return nil
}
