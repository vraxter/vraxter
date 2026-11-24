// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	maxMetaSkillReadBytes = 64000
	// MaxMediaReadBytes caps multimedia reads for vraxter-see and vraxter-hear at 10 MB.
	MaxMediaReadBytes int64 = 10 * 1024 * 1024
)

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

// ReadWorkspaceMediaFile reads an image or audio file strictly confined inside the active
// workspace via os.OpenRoot, enforces MaxMediaReadBytes (10 MB) via io.LimitReader, and
// validates real magic bytes rather than trusting file extensions.
func ReadWorkspaceMediaFile(targetPath, expectedKind string) ([]byte, string, error) {
	root, rel, err := openInWorkspace(targetPath)
	if err != nil {
		return nil, "", err
	}
	defer root.Close()

	f, err := root.Open(rel)
	if err != nil {
		return nil, "", fmt.Errorf("failed opening workspace media file '%s': %w", targetPath, err)
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, MaxMediaReadBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("failed reading workspace media file '%s': %w", targetPath, err)
	}
	if int64(len(data)) > MaxMediaReadBytes {
		return nil, "", fmt.Errorf("media file '%s' exceeds maximum allowed size (%d bytes)", targetPath, MaxMediaReadBytes)
	}
	if len(data) == 0 {
		return nil, "", fmt.Errorf("media file '%s' is empty", targetPath)
	}

	switch strings.ToLower(strings.TrimSpace(expectedKind)) {
	case "image":
		mimeType, ok := detectImageMagicBytes(data)
		if !ok {
			return nil, "", fmt.Errorf("file '%s' failed image magic-byte verification (only PNG, JPEG, GIF, and WebP are permitted)", targetPath)
		}
		return data, mimeType, nil
	case "audio":
		mimeType, ok := detectAudioMagicBytes(data)
		if !ok {
			return nil, "", fmt.Errorf("file '%s' failed audio magic-byte verification (only WAV, OGG, FLAC, MP3, and M4A/MP4 are permitted)", targetPath)
		}
		return data, mimeType, nil
	default:
		return nil, "", fmt.Errorf("unsupported media kind '%s'", expectedKind)
	}
}

func detectImageMagicBytes(data []byte) (string, bool) {
	if len(data) >= 8 && bytes.Equal(data[:8], []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}) {
		return "image/png", true
	}
	if len(data) >= 3 && data[0] == 0xFF && data[1] == 0xD8 && data[2] == 0xFF {
		return "image/jpeg", true
	}
	if len(data) >= 6 && (bytes.Equal(data[:6], []byte("GIF87a")) || bytes.Equal(data[:6], []byte("GIF89a"))) {
		return "image/gif", true
	}
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WEBP")) {
		return "image/webp", true
	}
	return "", false
}

func detectAudioMagicBytes(data []byte) (string, bool) {
	if len(data) >= 12 && bytes.Equal(data[:4], []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")) {
		return "audio/wav", true
	}
	if len(data) >= 4 && bytes.Equal(data[:4], []byte("OggS")) {
		return "audio/ogg", true
	}
	if len(data) >= 4 && bytes.Equal(data[:4], []byte("fLaC")) {
		return "audio/flac", true
	}
	if len(data) >= 3 && bytes.Equal(data[:3], []byte("ID3")) {
		return "audio/mpeg", true
	}
	if len(data) >= 2 && data[0] == 0xFF && (data[1]&0xE0) == 0xE0 && (data[1]&0x18) != 0x08 && (data[1]&0x06) != 0x00 {
		return "audio/mpeg", true
	}
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		brand := string(data[8:12])
		if brand == "M4A " || brand == "M4B " || brand == "mp42" || brand == "isom" || brand == "iso2" {
			return "audio/mp4", true
		}
	}
	return "", false
}
