// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package tests

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/patagonicrune/vraxter/internal/utils"
)

func createTestTarGz(path string, contentMap map[string]string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	gw := gzip.NewWriter(file)
	defer gw.Close()

	tw := tar.NewWriter(gw)
	defer tw.Close()

	for name, content := range contentMap {
		hdr := &tar.Header{
			Name: name,
			Mode: 0600,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			return err
		}
	}
	return nil
}

func createTestZip(path string, contentMap map[string]string) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	zw := zip.NewWriter(file)
	defer zw.Close()

	for name, content := range contentMap {
		f, err := zw.Create(name)
		if err != nil {
			return err
		}
		if _, err := f.Write([]byte(content)); err != nil {
			return err
		}
	}
	return nil
}

func TestArchive_ExtractTarGz(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "vraxter-archive-test-*")
	defer os.RemoveAll(tmpDir)

	tarPath := filepath.Join(tmpDir, "test.tar.gz")
	destPath := filepath.Join(tmpDir, "extracted-tar")

	filesMap := map[string]string{
		"file1.txt": "hello tar",
		"dir/f2.go": "package main",
	}

	err := createTestTarGz(tarPath, filesMap)
	if err != nil {
		t.Fatalf("failed to create test tar: %v", err)
	}

	err = utils.ExtractTarGz(tarPath, destPath)
	if err != nil {
		t.Fatalf("ExtractTarGz failed: %v", err)
	}

	for name, expectedContent := range filesMap {
		b, err := os.ReadFile(filepath.Join(destPath, name))
		if err != nil {
			t.Errorf("failed to read extracted file %s: %v", name, err)
		}
		if string(b) != expectedContent {
			t.Errorf("content mismatch in %s: expected %s, got %s", name, expectedContent, string(b))
		}
	}
}

func TestArchive_ExtractZip(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "vraxter-archive-test-*")
	defer os.RemoveAll(tmpDir)

	zipPath := filepath.Join(tmpDir, "test.zip")
	destPath := filepath.Join(tmpDir, "extracted-zip")

	filesMap := map[string]string{
		"config.json": `{"version": 1}`,
	}

	err := createTestZip(zipPath, filesMap)
	if err != nil {
		t.Fatalf("failed to create test zip: %v", err)
	}

	err = utils.ExtractZip(zipPath, destPath)
	if err != nil {
		t.Fatalf("ExtractZip failed: %v", err)
	}

	b, _ := os.ReadFile(filepath.Join(destPath, "config.json"))
	if string(b) != `{"version": 1}` {
		t.Errorf("zip content mismatch")
	}
}

func TestArchive_CleanupFolder(t *testing.T) {
	tmpDir, _ := os.MkdirTemp("", "vraxter-cleanup-test-*")
	
	// Create a single sub-folder with files to test SDK hoisting
	subDir := filepath.Join(tmpDir, "sub")
	os.MkdirAll(subDir, 0755)
	os.WriteFile(filepath.Join(subDir, "b.txt"), []byte("b"), 0644)

	err := utils.CleanupFolder(tmpDir)
	if err != nil {
		t.Fatalf("CleanupFolder failed: %v", err)
	}

	_, err = os.Stat(filepath.Join(tmpDir, "b.txt"))
	if os.IsNotExist(err) {
		t.Fatalf("Expected inner file b.txt to be hoisted to the root directory")
	}
}
