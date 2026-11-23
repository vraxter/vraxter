// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const maxMetaSkillReadBytes = 64000

var (
	workspaceRootMu sync.RWMutex
	workspaceRoot   string
)

// SetWorkspaceRoot binds the workspace directory used to confine vraxter-read-file,
// vraxter-list-dir, and vraxter-patch-code via os.OpenRoot. Passing "" resets to os.Getwd().
func SetWorkspaceRoot(dir string) {
	workspaceRootMu.Lock()
	defer workspaceRootMu.Unlock()
	workspaceRoot = strings.TrimSpace(dir)
}

// GetWorkspaceRoot returns the active absolute workspace root directory.
func GetWorkspaceRoot() (string, error) {
	workspaceRootMu.RLock()
	configured := workspaceRoot
	workspaceRootMu.RUnlock()

	if configured != "" {
		abs, err := filepath.Abs(configured)
		if err != nil {
			return "", err
		}
		return filepath.Clean(abs), nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Clean(cwd), nil
}

// openInWorkspace confines targetPath to the active workspace directory using os.OpenRoot,
// rejecting path traversal (".."), home directory expansion ("~"), outside absolute paths,
// and symlink escapes.
func openInWorkspace(targetPath string) (*os.Root, string, error) {
	wsDir, err := GetWorkspaceRoot()
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve workspace root: %w", err)
	}

	raw := strings.TrimSpace(targetPath)
	if strings.ContainsRune(raw, '\x00') {
		return nil, "", fmt.Errorf("path contains null byte")
	}
	if strings.HasPrefix(raw, "~") {
		return nil, "", fmt.Errorf("access denied: path '%s' escapes workspace root", targetPath)
	}

	var rel string
	if raw == "" || raw == "." {
		rel = "."
	} else if filepath.IsAbs(raw) {
		cleanedAbs := filepath.Clean(raw)
		r, err := filepath.Rel(wsDir, cleanedAbs)
		if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(os.PathSeparator)) || filepath.IsAbs(r) {
			return nil, "", fmt.Errorf("access denied: path '%s' is outside workspace root '%s'", targetPath, wsDir)
		}
		rel = r
	} else {
		cleaned := filepath.Clean(raw)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(os.PathSeparator)) || filepath.IsAbs(cleaned) {
			return nil, "", fmt.Errorf("access denied: path '%s' escapes workspace root", targetPath)
		}
		rel = cleaned
	}

	// Normalize to slash form accepted consistently by os.Root across platforms
	relSlash := filepath.ToSlash(rel)
	if relSlash == "" {
		relSlash = "."
	}

	root, err := os.OpenRoot(wsDir)
	if err != nil {
		return nil, "", fmt.Errorf("failed to open workspace root '%s': %w", wsDir, err)
	}
	return root, relSlash, nil
}

// ReadFile reads file content strictly confined to the active workspace via os.OpenRoot,
// capped at 64 KB to prevent context exhaustion.
func ReadFile(path string) string {
	root, rel, err := openInWorkspace(path)
	if err != nil {
		return fmt.Sprintf("vraxter-read-file ERROR: %v", err)
	}
	defer root.Close()

	f, err := root.Open(rel)
	if err != nil {
		return fmt.Sprintf("vraxter-read-file ERROR: %v", err)
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxMetaSkillReadBytes+1))
	if err != nil {
		return fmt.Sprintf("vraxter-read-file ERROR: %v", err)
	}

	if len(b) > maxMetaSkillReadBytes {
		return fmt.Sprintf("FILE: %s\nCONTENT (TRUNCATED):\n%s", path, string(b[:maxMetaSkillReadBytes]))
	}
	return fmt.Sprintf("FILE: %s\nCONTENT:\n%s", path, string(b))
}

// ListDir lists entries in a directory strictly confined to the active workspace via os.OpenRoot.
func ListDir(path string) string {
	if strings.TrimSpace(path) == "" {
		path = "."
	}
	root, rel, err := openInWorkspace(path)
	if err != nil {
		return fmt.Sprintf("vraxter-list-dir ERROR: %v", err)
	}
	defer root.Close()

	f, err := root.Open(rel)
	if err != nil {
		return fmt.Sprintf("vraxter-list-dir ERROR: %v", err)
	}
	defer f.Close()

	entries, err := f.ReadDir(-1)
	if err != nil {
		return fmt.Sprintf("vraxter-list-dir ERROR: %v", err)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("DIRECTORY LISTING FOR: %s\n", path))
	for _, e := range entries {
		t := "FILE"
		if e.IsDir() {
			t = "DIR "
		}
		sb.WriteString(fmt.Sprintf("[%s] %s\n", t, e.Name()))
	}
	return sb.String()
}

// PatchCode performs an exact search-and-replace edit strictly confined to the active workspace via os.OpenRoot.
func PatchCode(path, search, replace string) string {
	root, rel, err := openInWorkspace(path)
	if err != nil {
		return fmt.Sprintf("vraxter-patch-code ERROR reading file: %v", err)
	}
	defer root.Close()

	rf, err := root.Open(rel)
	if err != nil {
		return fmt.Sprintf("vraxter-patch-code ERROR reading file: %v", err)
	}
	b, readErr := io.ReadAll(rf)
	rf.Close()
	if readErr != nil {
		return fmt.Sprintf("vraxter-patch-code ERROR reading file: %v", readErr)
	}

	content := string(b)
	if !strings.Contains(content, search) {
		return "vraxter-patch-code ERROR: Target search string not found in file. Ensure exact matching including whitespace."
	}

	patched := strings.Replace(content, search, replace, 1)

	wf, err := root.OpenFile(rel, os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Sprintf("vraxter-patch-code ERROR applying patch: %v", err)
	}
	if _, err := wf.Write([]byte(patched)); err != nil {
		wf.Close()
		return fmt.Sprintf("vraxter-patch-code ERROR applying patch: %v", err)
	}
	if err := wf.Close(); err != nil {
		return fmt.Sprintf("vraxter-patch-code ERROR applying patch: %v", err)
	}

	return fmt.Sprintf("vraxter-patch-code SUCCESS: Replaced target block in %s", path)
}
