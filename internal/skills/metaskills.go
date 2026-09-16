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
	"os"
	"strings"
)

// expandPath replaces ~ with the user's home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			return strings.Replace(path, "~", home, 1)
		}
	}
	return path
}

// ReadFile reads the absolute or relative file content safely up to a 64kb limit.
func ReadFile(path string) string {
	path = expandPath(path)
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("vraxter-read-file ERROR: %v", err)
	}
	
	// Truncate to avoid context explosion if it's a massive binary/minified file
	if len(b) > 64000 {
		return fmt.Sprintf("FILE: %s\nCONTENT (TRUNCATED):\n%s", path, string(b[:64000]))
	}
	return fmt.Sprintf("FILE: %s\nCONTENT:\n%s", path, string(b))
}

// ListDir maps out the files in the directory.
func ListDir(path string) string {
	if path == "" {
		path = "."
	}
	path = expandPath(path)
	entries, err := os.ReadDir(path)
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

// PatchCode performs an exact string replacement block on a target file natively.
func PatchCode(path, search, replace string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("vraxter-patch-code ERROR reading file: %v", err)
	}

	content := string(b)
	if !strings.Contains(content, search) {
		return "vraxter-patch-code ERROR: Target search string not found in file. Ensure exact matching including whitespace."
	}

	patched := strings.Replace(content, search, replace, 1)
	
	if err := os.WriteFile(path, []byte(patched), 0644); err != nil {
		return fmt.Sprintf("vraxter-patch-code ERROR applying patch: %v", err)
	}

	return fmt.Sprintf("vraxter-patch-code SUCCESS: Replaced target block in %s", path)
}
