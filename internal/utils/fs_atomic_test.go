// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package utils

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type failingMidStreamReader struct {
	prefix []byte
	sent   bool
}

func (r *failingMidStreamReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		n := copy(p, r.prefix)
		return n, nil
	}
	return 0, errors.New("simulated mid-stream disk/IO failure")
}

func assertNoLeftoverTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("found leftover temporary file %s in %s", e.Name(), dir)
		}
	}
}

func TestAtomicWriteFile_SuccessPermissionsAndCleanOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "settings.json")

	// 1. Initial atomic creation
	payload1 := []byte(`{"isolation_mode":"airgap"}`)
	if err := AtomicWriteFile(target, payload1, 0600); err != nil {
		t.Fatalf("AtomicWriteFile initial failed: %v", err)
	}

	got1, err := os.ReadFile(target)
	if err != nil || string(got1) != string(payload1) {
		t.Fatalf("unexpected content after initial write: %q (err=%v)", string(got1), err)
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(target)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Fatalf("expected permissions 0600, got %o", fi.Mode().Perm())
		}
	}
	assertNoLeftoverTempFiles(t, dir)

	// 2. Atomic overwrite
	payload2 := []byte(`{"isolation_mode":"local_lan","require_skill_approval":true}`)
	if err := AtomicWriteFile(target, payload2, 0600); err != nil {
		t.Fatalf("AtomicWriteFile overwrite failed: %v", err)
	}
	got2, err := os.ReadFile(target)
	if err != nil || string(got2) != string(payload2) {
		t.Fatalf("unexpected content after overwrite: %q (err=%v)", string(got2), err)
	}
	assertNoLeftoverTempFiles(t, dir)
}

func TestAtomicWriteFromReader_PreservesPreviousContentOnMidWriteFailure(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "daemon.key")

	original := []byte("vrx_0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err := AtomicWriteFile(target, original, 0600); err != nil {
		t.Fatalf("seed original file: %v", err)
	}

	// Simulate an interrupted write halfway through streaming new data.
	badReader := &failingMidStreamReader{prefix: []byte("PARTIAL_CORRUPT_KEY")}
	err := AtomicWriteFromReader(target, badReader, 0, 0600)
	if err == nil {
		t.Fatal("expected AtomicWriteFromReader to fail on mid-stream reader error")
	}

	// Existing file MUST remain 100% intact and zero .tmp-* files may remain.
	after, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile after failed write: %v", err)
	}
	if string(after) != string(original) {
		t.Fatalf("existing file was corrupted by failed atomic write: got %q, want %q", string(after), string(original))
	}
	assertNoLeftoverTempFiles(t, dir)
}

func TestAtomicCopyFileBounded_PreservesTargetOnSizeViolationAndCopiesCleanly(t *testing.T) {
	dir := t.TempDir()
	srcGood := filepath.Join(dir, "good.wasm")
	srcHuge := filepath.Join(dir, "huge.wasm")
	dst := filepath.Join(dir, "installed.wasm")

	if err := os.WriteFile(srcGood, []byte("\x00asm\x01\x00\x00\x00good"), 0600); err != nil {
		t.Fatalf("write srcGood: %v", err)
	}
	if err := os.WriteFile(srcHuge, make([]byte, 2048), 0600); err != nil {
		t.Fatalf("write srcHuge: %v", err)
	}

	// 1. Copy valid binary within limit
	if err := AtomicCopyFileBounded(srcGood, dst, 1024, 0600); err != nil {
		t.Fatalf("AtomicCopyFileBounded valid copy failed: %v", err)
	}
	assertNoLeftoverTempFiles(t, dir)

	// 2. Attempt to overwrite dst with an oversized file (> 1024 bytes) -> must fail and preserve dst!
	if err := AtomicCopyFileBounded(srcHuge, dst, 1024, 0600); err == nil || !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Fatalf("expected oversized copy to fail, got: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil || string(got) != "\x00asm\x01\x00\x00\x00good" {
		t.Fatalf("oversized copy corrupted existing destination file: %q (err=%v)", string(got), err)
	}
	assertNoLeftoverTempFiles(t, dir)

	// 3. Also verify mid-stream size limit enforcement when reader exceeds maxBytes
	err = AtomicWriteFromReader(dst, io.LimitReader(strings.NewReader(strings.Repeat("X", 2048)), 2048), 512, 0600)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Fatalf("expected reader exceeding maxBytes to fail, got: %v", err)
	}
	gotAfter, _ := os.ReadFile(dst)
	if string(gotAfter) != "\x00asm\x01\x00\x00\x00good" {
		t.Fatalf("existing file was altered after maxBytes stream rejection: %q", string(gotAfter))
	}
	assertNoLeftoverTempFiles(t, dir)
}
