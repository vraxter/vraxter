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
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/utils"
)

// initGitRepo creates a throwaway git repository in a temp directory.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.email", "test@vraxter.local")
	run("config", "user.name", "Vraxter Test")
	// Prevent "dubious ownership" errors in newer git versions when run from /tmp
	run("config", "safe.directory", dir)

	// Initial commit so we have HEAD
	readme := filepath.Join(dir, "README.md")
	os.WriteFile(readme, []byte("# Test Repo\n"), 0644)
	run("add", ".")
	run("commit", "-m", "init")

	return dir
}

func TestCaptureGitContext_CleanRepo(t *testing.T) {
	dir := initGitRepo(t)
	result := utils.CaptureGitContext(dir)
	// git status -s on a clean repo emits zero bytes → CaptureGitContext returns "".
	// This is correct behavior. We just assert it doesn't produce an error string.
	if strings.HasPrefix(result, "ERROR") {
		t.Errorf("clean repo should not return an error, got: %q", result)
	}
}

func TestCaptureGitContext_DirtyRepo(t *testing.T) {
	dir := initGitRepo(t)

	// Make an uncommitted change
	dirtyFile := filepath.Join(dir, "main.go")
	os.WriteFile(dirtyFile, []byte("package main\n\nfunc main() {}\n"), 0644)

	result := utils.CaptureGitContext(dir)
	if result == "" {
		t.Error("expected non-empty git context for dirty repo")
	}
	if !strings.Contains(result, "WORKSPACE GIT CONTEXT") {
		t.Errorf("expected dirty context block, got: %q", result)
	}
	if !strings.Contains(result, "main.go") {
		t.Errorf("expected dirty file to appear in context, got: %q", result)
	}
}

func TestCaptureGitContext_NonGitDirectory(t *testing.T) {
	dir := t.TempDir() // Plain dir, no git init
	result := utils.CaptureGitContext(dir)
	// Non-git dirs should return empty string cleanly (no panic, no error propagation)
	if result != "" {
		t.Errorf("expected empty string for non-git dir, got: %q", result)
	}
}

func TestCaptureGitContext_EmptyPath_UsesCWD(t *testing.T) {
	// When passing empty string, it should fall back to CWD.
	// The Vraxter source dir itself IS a git repo, so we just ensure no crash.
	result := utils.CaptureGitContext("")
	// We just verify it doesn't panic and returns a string (may or may not be empty)
	_ = result
}

func TestCaptureGitContext_TrackedChange_HasDiff(t *testing.T) {
	dir := initGitRepo(t)

	// Modify the tracked file
	readme := filepath.Join(dir, "README.md")
	os.WriteFile(readme, []byte("# Test Repo\n\nModified!\n"), 0644)

	result := utils.CaptureGitContext(dir)
	if !strings.Contains(result, "UNCOMMITTED CHANGES") {
		t.Errorf("expected UNCOMMITTED CHANGES section for tracked modified file, got: %q", result)
	}
}
