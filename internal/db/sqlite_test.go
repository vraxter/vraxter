package db

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAutoBackup(t *testing.T) {
	// Create a temp directory for the test database
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vraxter.db")

	// Create dummy database file content
	dummyContent := []byte("vraxter sqlite test content")
	err := os.WriteFile(dbPath, dummyContent, 0644)
	if err != nil {
		t.Fatalf("failed to create dummy db file: %v", err)
	}

	// 1. Run autoBackup
	err = autoBackup(dbPath)
	if err != nil {
		t.Fatalf("autoBackup failed: %v", err)
	}

	// 2. Verify backup file was created
	backupsDir := filepath.Join(dir, "backups")
	timestamp := time.Now().Format("2006-01-02")
	backupName := fmt.Sprintf("vraxter_%s.db", timestamp)
	backupPath := filepath.Join(backupsDir, backupName)

	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("expected backup file to exist at %q, got error: %v", backupPath, err)
	}

	// 3. Verify permissions (0600) on non-Windows platforms
	mode := info.Mode().Perm()
	if os.PathSeparator == '/' && mode != 0600 {
		t.Errorf("expected backup file permissions to be 0600, got %v", mode)
	}

	// 4. Test Rolling Retention (fill folder with older mock files)
	// We'll create 10 older mock files, and verify autoBackup limits them to 7!
	for i := 1; i <= 10; i++ {
		// e.g. vraxter_2026-05-01.db to vraxter_2026-05-10.db
		mockName := fmt.Sprintf("vraxter_2026-05-%02d.db", i)
		mockPath := filepath.Join(backupsDir, mockName)
		_ = os.WriteFile(mockPath, []byte("old backup"), 0600)
	}

	// Run backup again (we must delete today's backup file first so it runs again!)
	_ = os.Remove(backupPath)
	err = autoBackup(dbPath)
	if err != nil {
		t.Fatalf("autoBackup failed on rolling test: %v", err)
	}

	// Verify that the backups directory contains exactly 7 backup files (6 old ones + 1 today's backup)
	files, err := os.ReadDir(backupsDir)
	if err != nil {
		t.Fatalf("failed to read backups dir: %v", err)
	}

	var backupFilesCount int
	for _, f := range files {
		if !f.IsDir() && strings.HasPrefix(f.Name(), "vraxter_") && strings.HasSuffix(f.Name(), ".db") {
			backupFilesCount++
		}
	}

	if backupFilesCount != 7 {
		t.Errorf("expected exactly 7 backups, got %d", backupFilesCount)
	}
}
