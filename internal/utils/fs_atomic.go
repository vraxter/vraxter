// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package utils

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// AtomicWriteFile writes data to destPath atomically by creating a temporary file
// in the same directory, flushing it to disk via Sync(), applying perm, renaming
// it over destPath, and syncing the parent directory on POSIX platforms.
// If any step fails, the temporary file is removed and any existing file at destPath
// remains untouched.
func AtomicWriteFile(destPath string, data []byte, perm os.FileMode) error {
	return AtomicWriteFromReader(destPath, bytes.NewReader(data), 0, perm)
}

// AtomicWriteFromReader streams r into a temporary file in filepath.Dir(destPath),
// optionally enforcing maxBytes (when maxBytes > 0), flushes to storage, sets perm,
// and atomically renames the file to destPath.
func AtomicWriteFromReader(destPath string, r io.Reader, maxBytes int64, perm os.FileMode) error {
	if perm == 0 {
		perm = 0600
	}

	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0700); err != nil {
		return fmt.Errorf("failed to ensure target directory %s: %w", destDir, err)
	}

	tmpFile, err := os.CreateTemp(destDir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary file in %s: %w", destDir, err)
	}
	tmpPath := tmpFile.Name()
	closed := false
	cleanup := true
	defer func() {
		if !closed {
			_ = tmpFile.Close()
		}
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	reader := r
	if maxBytes > 0 {
		reader = io.LimitReader(r, maxBytes+1)
	}

	written, err := io.Copy(tmpFile, reader)
	if err != nil {
		return fmt.Errorf("failed writing temporary file for %s: %w", destPath, err)
	}
	if maxBytes > 0 && written > maxBytes {
		return fmt.Errorf("file %s exceeds maximum allowed size (%d bytes)", destPath, maxBytes)
	}

	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("failed syncing temporary file for %s: %w", destPath, err)
	}
	if err := tmpFile.Close(); err != nil {
		closed = true
		return fmt.Errorf("failed closing temporary file for %s: %w", destPath, err)
	}
	closed = true

	if err := os.Chmod(tmpPath, perm); err != nil {
		return fmt.Errorf("failed setting permissions on temporary file for %s: %w", destPath, err)
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("failed atomically replacing %s: %w", destPath, err)
	}
	cleanup = false

	_ = os.Chmod(destPath, perm)
	syncParentDir(destDir)
	return nil
}

// AtomicCopyFile copies srcPath to destPath atomically with target permissions perm.
func AtomicCopyFile(srcPath, destPath string, perm os.FileMode) error {
	return AtomicCopyFileBounded(srcPath, destPath, 0, perm)
}

// AtomicCopyFileBounded copies srcPath to destPath atomically while enforcing an optional
// maxBytes size limit (when maxBytes > 0).
func AtomicCopyFileBounded(srcPath, destPath string, maxBytes int64, perm os.FileMode) error {
	srcFile, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	fi, err := srcFile.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("source path %s is not a regular file", srcPath)
	}
	if maxBytes > 0 && fi.Size() > maxBytes {
		return fmt.Errorf("file %s exceeds maximum allowed size (%d > %d bytes)", srcPath, fi.Size(), maxBytes)
	}

	return AtomicWriteFromReader(destPath, srcFile, maxBytes, perm)
}

func syncParentDir(dir string) {
	if runtime.GOOS == "windows" {
		return
	}
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	_ = d.Close()
}
