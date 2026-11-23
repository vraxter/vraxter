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
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Pure Go SQLite driver without CGO
)

// Store manages the core SQLite connection and schemas
type Store struct {
	Conn *sql.DB
}

// NewStore initializes the database connection, durability PRAGMAs, WAL-safe VACUUM INTO backup, and schema.
func NewStore(dbPath string) (*Store, error) {
	existedBefore := false
	if dbPath != ":memory:" && dbPath != "" {
		if info, err := os.Stat(dbPath); err == nil && info.Size() > 0 {
			existedBefore = true
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("could not open db: %w", err)
	}

	// Pin to a single connection BEFORE applying connection-scoped PRAGMAs (foreign_keys, busy_timeout, synchronous)
	// so every query inherits the exact durability and referential integrity configuration.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("could not ping db: %w", err)
	}

	// Durability + Integrity + Performance PRAGMAs (WAL Mode + Synchronous FULL + FK enforcement)
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=FULL;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA cache_size=-32000;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA mmap_size=134217728;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("could not set pragma %s: %w", p, err)
		}
	}

	// Secure-by-Design: Perform transactionally consistent WAL-aware backup via VACUUM INTO
	if existedBefore {
		if err := autoBackup(db, dbPath); err != nil {
			log.Printf("Warning: SQLite VACUUM INTO autoBackup failed: %v", err)
		}
	}

	store := &Store{Conn: db}

	// Bootstrap required tables
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to init db schema: %w", err)
	}

	if dbPath != ":memory:" && dbPath != "" {
		_ = os.Chmod(dbPath, 0600)
	}

	return store, nil
}

func (s *Store) initSchema() error {
	const schema = `
	CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		language TEXT NOT NULL DEFAULT 'en',
		theme_preference TEXT NOT NULL DEFAULT 'system',
		expertise TEXT DEFAULT '',
		interests TEXT DEFAULT '',
		bio TEXT DEFAULT '',
		registered_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS skills (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT,
		version TEXT NOT NULL,
		command TEXT NOT NULL,
		language TEXT NOT NULL,
		engine TEXT NOT NULL DEFAULT 'wasm',
		tier INTEGER NOT NULL DEFAULT 3,
		score REAL NOT NULL DEFAULT 0.0,
		is_official BOOLEAN NOT NULL DEFAULT 0,
		checksum TEXT NOT NULL,
		permissions TEXT NOT NULL DEFAULT '',
		downloads INTEGER NOT NULL DEFAULT 0,
		keywords TEXT NOT NULL DEFAULT '[]',
		examples TEXT NOT NULL DEFAULT '[]',
		tags TEXT NOT NULL DEFAULT '[]',
		param_regex TEXT NOT NULL DEFAULT '',
		params_schema TEXT DEFAULT '',
		timeout_seconds INTEGER NOT NULL DEFAULT 0,
		manifest_v1 TEXT DEFAULT '',
		vector BLOB DEFAULT NULL,
		installed_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS conversations (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
		summary TEXT DEFAULT '',
		specialist_id TEXT DEFAULT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS messages (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		role TEXT NOT NULL,
		content TEXT NOT NULL,
		tokens_used INTEGER DEFAULT 0,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(conversation_id) REFERENCES conversations(id)
	);
	
	-- Composite index for fast chat history retrieval
	CREATE INDEX IF NOT EXISTS idx_messages_conv_time ON messages (conversation_id, timestamp);

	CREATE TABLE IF NOT EXISTS interactions (
		id TEXT PRIMARY KEY,
		intent_query TEXT NOT NULL,
		skill_id TEXT,
		status TEXT NOT NULL,
		result_output TEXT,
		error_msg TEXT,
		timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(skill_id) REFERENCES skills(id)
	);

	CREATE TABLE IF NOT EXISTS providers (
		id TEXT PRIMARY KEY,
		name TEXT UNIQUE NOT NULL,
		type TEXT NOT NULL, -- openai, google, anthropic, ollama
		api_key TEXT,
		base_url TEXT,
		is_active BOOLEAN DEFAULT 1,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS models (
		id TEXT PRIMARY KEY,
		provider_id TEXT NOT NULL,
		alias TEXT NOT NULL,
		model TEXT NOT NULL,
		priority INTEGER DEFAULT 99,
		is_active BOOLEAN DEFAULT 1,
		capabilities TEXT DEFAULT '',
		context_window INTEGER DEFAULT 8192,
		use_cases TEXT DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(provider_id) REFERENCES providers(id)
	);

	CREATE TABLE IF NOT EXISTS specialists (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		expertise TEXT NOT NULL,
		model_id TEXT,
		system_prompt TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS embeddings (
		id TEXT PRIMARY KEY,
		conversation_id TEXT NOT NULL,
		text_content TEXT NOT NULL,
		vector BLOB NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(conversation_id) REFERENCES conversations(id)
	);
	CREATE INDEX IF NOT EXISTS idx_embeddings_conv ON embeddings (conversation_id);

	CREATE TABLE IF NOT EXISTS api_keys (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		key_hash TEXT UNIQUE NOT NULL,
		scopes TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	`
	_, err := s.Conn.Exec(schema)
	if err != nil {
		log.Printf("DB Schema Init error: %v", err)
	}

	// Migration: Add params_schema to skills if it doesn't exist
	_, _ = s.Conn.Exec("ALTER TABLE skills ADD COLUMN params_schema TEXT DEFAULT '';")
	_, _ = s.Conn.Exec("ALTER TABLE skills ADD COLUMN timeout_seconds INTEGER NOT NULL DEFAULT 0;")
	_, _ = s.Conn.Exec("ALTER TABLE skills ADD COLUMN manifest_v1 TEXT DEFAULT '';")
	
	// Migration: Add use_case_priorities to models
	_, _ = s.Conn.Exec("ALTER TABLE models ADD COLUMN use_case_priorities TEXT DEFAULT '{}';")

	return err
}

func (s *Store) Close() error {
	return s.Conn.Close()
}

func autoBackup(conn *sql.DB, dbPath string) error {
	backupsDir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(backupsDir, 0700); err != nil {
		return err
	}

	timestamp := time.Now().Format("2006-01-02")
	backupName := fmt.Sprintf("vraxter_%s.db", timestamp)
	backupPath := filepath.Join(backupsDir, backupName)

	// Skip if a backup for today has already been created to reduce write cycles
	if _, err := os.Stat(backupPath); err == nil {
		return nil
	}

	// Use SQLite VACUUM INTO on a temp file to produce a WAL-consistent, compacted snapshot,
	// then restrict permissions to 0600 and atomically move into place.
	tmpPath := backupPath + ".tmp"
	_ = os.Remove(tmpPath)

	if _, err := conn.Exec("VACUUM INTO ?", tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("VACUUM INTO failed: %w", err)
	}

	if err := os.Chmod(tmpPath, 0600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, backupPath); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	// Rolling retention: maintain the last 7 daily backups
	files, err := os.ReadDir(backupsDir)
	if err == nil {
		var backupFiles []os.DirEntry
		for _, f := range files {
			if !f.IsDir() && strings.HasPrefix(f.Name(), "vraxter_") && strings.HasSuffix(f.Name(), ".db") {
				backupFiles = append(backupFiles, f)
			}
		}

		if len(backupFiles) > 7 {
			sort.Slice(backupFiles, func(i, j int) bool {
				return backupFiles[i].Name() < backupFiles[j].Name()
			})
			for i := 0; i < len(backupFiles)-7; i++ {
				_ = os.Remove(filepath.Join(backupsDir, backupFiles[i].Name()))
			}
		}
	}

	return nil
}
