// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestExtractTarXz_Success(t *testing.T) {
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "src")
	err := os.Mkdir(sourceDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create src dir: %v", err)
	}
	
	testFile := filepath.Join(sourceDir, "test.txt")
	err = os.WriteFile(testFile, []byte("hello xz"), 0644)
	if err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	archivePath := filepath.Join(tempDir, "dummy.tar.xz")
	cmd := exec.Command("tar", "-cJf", archivePath, "-C", sourceDir, "test.txt")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("tar -cJf failed, host might not have xz installed. Skipping test. Out: %s", string(out))
	}

	destDir := filepath.Join(tempDir, "dest")
	err = os.Mkdir(destDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create dest dir: %v", err)
	}

	err = ExtractTarXz(archivePath, destDir)
	if err != nil {
		t.Fatalf("ExtractTarXz failed: %v", err)
	}

	extractedFile := filepath.Join(destDir, "test.txt")
	content, err := os.ReadFile(extractedFile)
	if err != nil {
		t.Fatalf("Failed to read extracted file: %v", err)
	}

	if string(content) != "hello xz" {
		t.Errorf("Expected 'hello xz', got %s", string(content))
	}
}

func TestExtractTarXz_InvalidArchive(t *testing.T) {
	tempDir := t.TempDir()
	invalidPath := filepath.Join(tempDir, "invalid.tar.xz")
	err := os.WriteFile(invalidPath, []byte("not an archive"), 0644)
	if err != nil {
		t.Fatalf("Failed to create invalid file: %v", err)
	}

	destDir := filepath.Join(tempDir, "dest")
	err = os.Mkdir(destDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create dest dir: %v", err)
	}

	err = ExtractTarXz(invalidPath, destDir)
	if err == nil {
		t.Error("Expected an error extracting an invalid tar.xz file, got nil")
	}
}
