// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// ErrDatabaseLocked is returned when another Vraxter process or Store instance
// already holds the exclusive lock for the target SQLite database.
var ErrDatabaseLocked = errors.New("database is locked by another running Vraxter process")

var (
	activeDBLocksMu sync.Mutex
	activeDBLocks   = make(map[string]struct{})
)

type dbFileLock struct {
	absPath  string
	file     *os.File
	mu       sync.Mutex
	released bool
}

// acquireDBFileLock obtains a non-blocking exclusive OS file lock on "<dbPath>.lock"
// (for example, vraxter.db.lock) and also tracks active handles in-process to prevent
// concurrent opens or premature deletion of .vraxter.db.working.
func acquireDBFileLock(dbPath string) (*dbFileLock, error) {
	lockPath := filepath.Clean(dbPath) + ".lock"
	absPath, err := filepath.Abs(lockPath)
	if err != nil {
		absPath = lockPath
	}

	if err := os.MkdirAll(filepath.Dir(absPath), 0700); err != nil {
		return nil, fmt.Errorf("failed creating database lock directory: %w", err)
	}

	activeDBLocksMu.Lock()
	if _, exists := activeDBLocks[absPath]; exists {
		activeDBLocksMu.Unlock()
		return nil, fmt.Errorf("%w: %s", ErrDatabaseLocked, absPath)
	}
	activeDBLocks[absPath] = struct{}{}
	activeDBLocksMu.Unlock()

	releaseInProc := func() {
		activeDBLocksMu.Lock()
		delete(activeDBLocks, absPath)
		activeDBLocksMu.Unlock()
	}

	f, err := os.OpenFile(absPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		releaseInProc()
		return nil, fmt.Errorf("failed opening database lock file %s: %w", absPath, err)
	}
	_ = os.Chmod(absPath, 0600)

	if err := lockFileExclusiveNonBlocking(f); err != nil {
		_ = f.Close()
		releaseInProc()
		return nil, fmt.Errorf("%w: %s (%v)", ErrDatabaseLocked, absPath, err)
	}

	// Best-effort diagnostic PID marker inside the held lockfile.
	_ = f.Truncate(0)
	_, _ = f.Seek(0, 0)
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Sync()

	return &dbFileLock{
		absPath: absPath,
		file:    f,
	}, nil
}

// Release frees the OS file lock and in-process ownership slot.
// The lock file itself is intentionally kept on disk (0600) to avoid Unix flock inode races.
func (l *dbFileLock) Release() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	if l.released {
		l.mu.Unlock()
		return nil
	}
	l.released = true
	f := l.file
	l.file = nil
	absPath := l.absPath
	l.mu.Unlock()

	var unlockErr error
	if f != nil {
		unlockErr = unlockFileExclusive(f)
		if closeErr := f.Close(); closeErr != nil && unlockErr == nil {
			unlockErr = closeErr
		}
	}

	activeDBLocksMu.Lock()
	delete(activeDBLocks, absPath)
	activeDBLocksMu.Unlock()

	return unlockErr
}
