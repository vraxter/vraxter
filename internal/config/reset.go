// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// PerformFactoryReset resets Vraxter to factory settings.
// If createBackup is true, the current AppDir is archived/backed up to AppDir.backup-YYYYMMDD-HHMMSS
// before being purged. Returns the path to the backup created (if any) and any error encountered.
func PerformFactoryReset(createBackup bool) (string, error) {
	cfg := Load()
	appDir := cfg.AppDir
	if appDir == "" {
		return "", fmt.Errorf("app directory is not configured")
	}

	var backupPath string
	if createBackup {
		timestamp := time.Now().Format("20060102-150405")
		backupPath = filepath.Clean(appDir) + ".backup-" + timestamp
		// Attempt atomic rename first
		err := os.Rename(appDir, backupPath)
		if err != nil {
			// Fallback: copy directory recursively
			if cpErr := copyDir(appDir, backupPath); cpErr != nil {
				return "", fmt.Errorf("failed to create backup before reset: %w", cpErr)
			}
			_ = os.RemoveAll(appDir)
		}
	} else {
		_ = os.RemoveAll(appDir)
	}

	// Re-create the clean AppDir with restricted permissions
	if err := os.MkdirAll(appDir, 0700); err != nil {
		return backupPath, fmt.Errorf("failed to recreate app directory: %w", err)
	}
	_ = os.MkdirAll(filepath.Join(appDir, "skills"), 0700)
	_ = os.MkdirAll(filepath.Join(appDir, "sdk"), 0700)

	return backupPath, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0700); err != nil {
		return err
	}

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

