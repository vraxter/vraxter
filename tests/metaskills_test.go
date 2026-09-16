// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/skills"
)

// ─── ReadFile ─────────────────────────────────────────────────────────────────

func TestReadFile_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	os.WriteFile(path, []byte("Hello, Vraxter!"), 0644)

	result := skills.ReadFile(path)
	if !strings.Contains(result, "Hello, Vraxter!") {
		t.Errorf("expected file content in result, got: %q", result)
	}
	if !strings.Contains(result, "FILE:") {
		t.Errorf("expected FILE: header in result, got: %q", result)
	}
}

func TestReadFile_FileNotFound(t *testing.T) {
	result := skills.ReadFile("/nonexistent/path/file.go")
	if !strings.HasPrefix(result, "vraxter-read-file ERROR") {
		t.Errorf("expected error prefix, got: %q", result)
	}
}

func TestReadFile_LargeFileTruncated(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.bin")

	// Write 65kb of data (above 64000 byte limit)
	bigData := make([]byte, 65000)
	for i := range bigData {
		bigData[i] = 'A'
	}
	os.WriteFile(path, bigData, 0644)

	result := skills.ReadFile(path)
	if !strings.Contains(result, "TRUNCATED") {
		t.Errorf("expected TRUNCATED indicator for large file, got result of length %d", len(result))
	}
}

// ─── ListDir ──────────────────────────────────────────────────────────────────

func TestListDir_Success(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0644)
	os.Mkdir(filepath.Join(dir, "internal"), 0755)

	result := skills.ListDir(dir)
	if !strings.Contains(result, "DIRECTORY LISTING") {
		t.Errorf("expected DIRECTORY LISTING header, got: %q", result)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected main.go in listing, got: %q", result)
	}
	if !strings.Contains(result, "internal") {
		t.Errorf("expected 'internal' dir in listing, got: %q", result)
	}
	if !strings.Contains(result, "[DIR ]") {
		t.Errorf("expected [DIR ] marker for subdirectory, got: %q", result)
	}
	if !strings.Contains(result, "[FILE]") {
		t.Errorf("expected [FILE] marker for file, got: %q", result)
	}
}

func TestListDir_EmptyPath_UsesCurrentDir(t *testing.T) {
	// Empty path defaults to "."
	result := skills.ListDir("")
	if !strings.Contains(result, "DIRECTORY LISTING") {
		t.Errorf("expected DIRECTORY LISTING for empty path, got: %q", result)
	}
}

func TestListDir_NonExistentPath(t *testing.T) {
	result := skills.ListDir("/nonexistent/path/here")
	if !strings.HasPrefix(result, "vraxter-list-dir ERROR") {
		t.Errorf("expected error prefix for nonexistent dir, got: %q", result)
	}
}

// ─── PatchCode ────────────────────────────────────────────────────────────────

func TestPatchCode_Success(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("func hello() {\n\treturn \"world\"\n}\n"), 0644)

	result := skills.PatchCode(path, "\"world\"", "\"Vraxter\"")
	if !strings.Contains(result, "SUCCESS") {
		t.Errorf("expected SUCCESS in result, got: %q", result)
	}

	// Verify the file was actually changed
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "Vraxter") {
		t.Errorf("expected patched content, got: %q", string(content))
	}
	if strings.Contains(string(content), "\"world\"") {
		t.Errorf("old content should have been replaced, but still present")
	}
}

func TestPatchCode_FileNotFound(t *testing.T) {
	result := skills.PatchCode("/nonexistent/file.go", "old", "new")
	if !strings.Contains(result, "ERROR reading file") {
		t.Errorf("expected file read error, got: %q", result)
	}
}

func TestPatchCode_SearchNotFound(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	os.WriteFile(path, []byte("func hello() {}\n"), 0644)

	result := skills.PatchCode(path, "func nonexistent() {}", "new code")
	if !strings.Contains(result, "Target search string not found") {
		t.Errorf("expected 'not found' error, got: %q", result)
	}
}

func TestPatchCode_OnlyReplacesFirstOccurrence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.go")
	original := "x := 1\ny := x\nz := x\n"
	os.WriteFile(path, []byte(original), 0644)

	result := skills.PatchCode(path, "x", "10")
	if !strings.Contains(result, "SUCCESS") {
		t.Fatalf("expected success, got: %q", result)
	}

	content, _ := os.ReadFile(path)
	// Only the first "x" should be replaced
	if strings.HasPrefix(string(content), "10 := 1") {
		// Correct: first occurrence replaced
	}
	// The file should still have "x" for the second/third occurrence
	count := strings.Count(string(content), "x")
	if count == 0 {
		t.Error("expected at least one remaining 'x' after single replacement")
	}
}
