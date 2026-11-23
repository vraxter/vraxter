// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewStore_DurabilityAndIntegrityPragmas(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "durability.db")

	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}
	defer store.Close()

	var syncMode int
	if err := store.Conn.QueryRow("PRAGMA synchronous;").Scan(&syncMode); err != nil {
		t.Fatalf("failed to read PRAGMA synchronous: %v", err)
	}
	if syncMode != 2 { // 2 == FULL
		t.Errorf("expected PRAGMA synchronous=FULL (2), got %d", syncMode)
	}

	var fkEnabled int
	if err := store.Conn.QueryRow("PRAGMA foreign_keys;").Scan(&fkEnabled); err != nil {
		t.Fatalf("failed to read PRAGMA foreign_keys: %v", err)
	}
	if fkEnabled != 1 {
		t.Errorf("expected PRAGMA foreign_keys=ON (1), got %d", fkEnabled)
	}

	var busyTimeout int
	if err := store.Conn.QueryRow("PRAGMA busy_timeout;").Scan(&busyTimeout); err != nil {
		t.Fatalf("failed to read PRAGMA busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("expected PRAGMA busy_timeout=5000, got %d", busyTimeout)
	}

	var journalMode string
	if err := store.Conn.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("failed to read PRAGMA journal_mode: %v", err)
	}
	if strings.ToLower(journalMode) != "wal" {
		t.Errorf("expected PRAGMA journal_mode=wal, got %q", journalMode)
	}

	// Verify foreign_keys enforcement rejects orphan messages referencing non-existent conversation
	_, err = store.Conn.Exec(`INSERT INTO messages (id, conversation_id, role, content) VALUES ('m1', 'nonexistent-conv', 'user', 'orphan')`)
	if err == nil {
		t.Fatalf("expected foreign key constraint failure for orphan message, but insert succeeded")
	}
}

func TestAutoBackup_VacuumIntoWALConsistent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "vraxter.db")

	// Create a real WAL-mode SQLite DB and write a committed row
	store, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("failed to create initial store: %v", err)
	}
	if _, err := store.Conn.Exec(`INSERT INTO conversations (id, title) VALUES ('conv-wal', 'WAL Critical Data')`); err != nil {
		store.Close()
		t.Fatalf("failed to insert test row: %v", err)
	}

	// 1. Run autoBackup via VACUUM INTO while connection is live in WAL mode
	if err := autoBackup(store.Conn, dbPath); err != nil {
		store.Close()
		t.Fatalf("autoBackup failed: %v", err)
	}

	// 2. Verify backup file was created and contains committed WAL data cleanly
	backupsDir := filepath.Join(dir, "backups")
	timestamp := time.Now().Format("2006-01-02")
	backupName := fmt.Sprintf("vraxter_%s.db", timestamp)
	backupPath := filepath.Join(backupsDir, backupName)

	info, err := os.Stat(backupPath)
	if err != nil {
		store.Close()
		t.Fatalf("expected backup file to exist at %q, got error: %v", backupPath, err)
	}

	// 3. Verify permissions (0600) on non-Windows platforms
	mode := info.Mode().Perm()
	if os.PathSeparator == '/' && mode != 0600 {
		t.Errorf("expected backup file permissions to be 0600, got %v", mode)
	}

	// 4. Open backup DB independently and verify WAL data was captured by VACUUM INTO
	backupConn, err := sql.Open("sqlite", backupPath)
	if err != nil {
		store.Close()
		t.Fatalf("failed to open backup sqlite db: %v", err)
	}
	var title string
	if err := backupConn.QueryRow(`SELECT title FROM conversations WHERE id = 'conv-wal'`).Scan(&title); err != nil {
		backupConn.Close()
		store.Close()
		t.Fatalf("failed to query committed row from VACUUM INTO backup: %v", err)
	}
	backupConn.Close()
	if title != "WAL Critical Data" {
		t.Errorf("expected 'WAL Critical Data' in backup, got %q", title)
	}

	// 5. Test Rolling Retention (fill folder with 10 older mock files, verify autoBackup limits to 7)
	for i := 1; i <= 10; i++ {
		mockName := fmt.Sprintf("vraxter_2026-05-%02d.db", i)
		mockPath := filepath.Join(backupsDir, mockName)
		_ = os.WriteFile(mockPath, []byte("old backup"), 0600)
	}

	_ = os.Remove(backupPath)
	if err := autoBackup(store.Conn, dbPath); err != nil {
		store.Close()
		t.Fatalf("autoBackup failed on rolling test: %v", err)
	}
	store.Close()

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
