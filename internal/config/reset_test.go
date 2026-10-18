// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vraxter/vraxter/internal/config"
)

func TestPerformFactoryReset(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "vraxter-test-reset-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	os.Setenv("VRAXTER_APP_DIR", tempDir)
	defer os.Unsetenv("VRAXTER_APP_DIR")

	// Create dummy files and directories in temp app dir
	dummyDB := filepath.Join(tempDir, "vraxter.db")
	dummyKey := filepath.Join(tempDir, ".masterkey")
	dummySettings := filepath.Join(tempDir, "settings.json")
	_ = os.WriteFile(dummyDB, []byte("dummy-db-content"), 0600)
	_ = os.WriteFile(dummyKey, []byte("dummy-key-content"), 0600)
	_ = os.WriteFile(dummySettings, []byte(`{"hub_url":"test"}`), 0600)
	_ = os.MkdirAll(filepath.Join(tempDir, "skills"), 0700)

	backupPath, err := config.PerformFactoryReset(true)
	if err != nil {
		t.Fatalf("PerformFactoryReset failed: %v", err)
	}

	if backupPath == "" {
		t.Errorf("expected backupPath to be populated, got empty")
	}

	// Verify backup exists and contains the dummy file
	backupDB := filepath.Join(backupPath, "vraxter.db")
	if _, err := os.Stat(backupDB); os.IsNotExist(err) {
		t.Errorf("expected backup file %s to exist", backupDB)
	}
	defer os.RemoveAll(backupPath)

	// Verify tempDir was recreated clean (without old db and masterkey)
	if _, err := os.Stat(dummyDB); err == nil {
		t.Errorf("expected old dummy db to be wiped from appDir")
	}
	if _, err := os.Stat(dummyKey); err == nil {
		t.Errorf("expected old dummy key to be wiped from appDir")
	}

	// Verify skills and sdk directories exist
	if _, err := os.Stat(filepath.Join(tempDir, "skills")); os.IsNotExist(err) {
		t.Errorf("expected skills dir to be recreated")
	}
	if _, err := os.Stat(filepath.Join(tempDir, "sdk")); os.IsNotExist(err) {
		t.Errorf("expected sdk dir to be recreated")
	}
}

