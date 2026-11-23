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
	"fmt"
	"net/http"
	"time"
)

// DefaultDownloadTimeout bounds full SDK/artifact downloads on DDIL links while
// NewDDILHTTPClient enforces 10s connect/TLS and 30s response header timeouts.
const DefaultDownloadTimeout = 10 * time.Minute

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
