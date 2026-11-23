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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDownloadFile_AtomicSuccessAndNoCorruptionOnHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("vraxter-sdk-payload"))
		case "/fail":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
		case "/hang":
			select {
			case <-r.Context().Done():
			case <-time.After(5 * time.Second):
			}
		}
	}))
	defer srv.Close()

	t.Run("Successful atomic download leaves no temp files", func(t *testing.T) {
		dir := t.TempDir()
		dest := filepath.Join(dir, "sdk.tar.gz")
		if err := DownloadFile(dest, srv.URL+"/ok"); err != nil {
			t.Fatalf("DownloadFile failed: %v", err)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read downloaded file: %v", err)
		}
		if string(got) != "vraxter-sdk-payload" {
			t.Fatalf("unexpected downloaded payload: %q", string(got))
		}
		entries, _ := os.ReadDir(dir)
		if len(entries) != 1 || entries[0].Name() != "sdk.tar.gz" {
			t.Fatalf("expected only final artifact in dir, got %d entries", len(entries))
		}
	})

	t.Run("Non-200 status preserves existing destination file untouched", func(t *testing.T) {
		dir := t.TempDir()
		dest := filepath.Join(dir, "existing.tar.gz")
		if err := os.WriteFile(dest, []byte("original-good-data"), 0600); err != nil {
			t.Fatalf("seed existing file: %v", err)
		}
		err := DownloadFile(dest, srv.URL+"/fail")
		if err == nil || !strings.Contains(err.Error(), "bad status") {
			t.Fatalf("expected bad status error, got: %v", err)
		}
		got, err := os.ReadFile(dest)
		if err != nil {
			t.Fatalf("read existing file: %v", err)
		}
		if string(got) != "original-good-data" {
			t.Fatalf("failed download corrupted existing destination file: %q", string(got))
		}
	})

	t.Run("Respects context cancellation on DDIL stalled links", func(t *testing.T) {
		dir := t.TempDir()
		dest := filepath.Join(dir, "stalled.tar.gz")
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()

		err := DownloadFileWithContext(ctx, dest, srv.URL+"/hang")
		if err == nil {
			t.Fatal("expected timeout/cancellation error on stalled download")
		}
		if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
			t.Fatalf("stalled download must not create partial destination file")
		}
	})
}
