package db

import (
	"database/sql"
	"fmt"
	"log"

	_ "modernc.org/sqlite" // Pure Go SQLite driver without CGO
)

// Store manages the core SQLite connection and schemas
type Store struct {
	Conn *sql.DB
}

// NewStore initializes the database connection and performance modes
func NewStore(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("could not open db: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("could not ping db: %w", err)
	}

	// Performance (WAL Mode + Optimization Pragmas)
	// Vital for Vraxter concurrent local performance
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA cache_size=-32000;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA mmap_size=134217728;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return nil, fmt.Errorf("could not set pragma %s: %w", p, err)
		}
	}

	store := &Store{Conn: db}

	// Bootstrap required tables
	if err := store.initSchema(); err != nil {
		return nil, fmt.Errorf("failed to init db schema: %w", err)
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
		installed_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS conversations (
		id TEXT PRIMARY KEY,
		title TEXT NOT NULL,
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

	CREATE TABLE IF NOT EXISTS models (
		id TEXT PRIMARY KEY,
		alias TEXT NOT NULL,
		provider TEXT NOT NULL,
		model TEXT NOT NULL,
		api_key TEXT,
		base_url TEXT,
		priority INTEGER DEFAULT 99,
		is_active BOOLEAN DEFAULT 1,
		capabilities TEXT DEFAULT '',
		context_window INTEGER DEFAULT 8192
	);

	CREATE TABLE IF NOT EXISTS specialists (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL,
		expertise TEXT NOT NULL,
		model_id TEXT,
		system_prompt TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	`
	_, err := s.Conn.Exec(schema)
	if err != nil {
		log.Printf("DB Schema Init error: %v", err)
	}

	// Simple migrations for existing databases (Phase 12.10 & 12.11)
	// These will fail safely if the columns already exist
	_, _ = s.Conn.Exec("ALTER TABLE skills ADD COLUMN permissions TEXT NOT NULL DEFAULT ''")
	_, _ = s.Conn.Exec("ALTER TABLE skills ADD COLUMN downloads INTEGER NOT NULL DEFAULT 0")

	// Migration for Phase 16 (Specialists)
	_, _ = s.Conn.Exec("ALTER TABLE conversations ADD COLUMN specialist_id TEXT DEFAULT NULL")

	// Migration for Phase 16.2 (Model Capabilities)
	_, _ = s.Conn.Exec("ALTER TABLE models ADD COLUMN capabilities TEXT DEFAULT ''")
	_, _ = s.Conn.Exec("ALTER TABLE models ADD COLUMN context_window INTEGER DEFAULT 0")

	return err
}

func (s *Store) Close() error {
	return s.Conn.Close()
}
