// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package db

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/utils"
	_ "modernc.org/sqlite" // Pure Go SQLite driver without CGO
)

// ErrEncryptionUnavailable is returned when at-rest field/blob encryption cannot be
// performed because the store's cryptographic service is missing or uninitialized.
var ErrEncryptionUnavailable = errors.New("database at-rest encryption service is unavailable")

// StoreOptions configures at-rest encryption and key management for a SQLite Store.
type StoreOptions struct {
	EncryptAtRest bool
	Crypto        *security.CryptoService
	MasterKeyPath string
}

// Store manages the core SQLite connection, schema, exclusive file lock, and at-rest AES-256-GCM encryption.
type Store struct {
	Conn          *sql.DB
	dbPath        string
	workingPath   string
	sealedEncPath string
	encryptAtRest bool
	crypto        *security.CryptoService
	fileLock      *dbFileLock
}

// NewStore initializes the database connection, durability PRAGMAs, WAL-safe VACUUM INTO backup, and schema.
func NewStore(dbPath string) (*Store, error) {
	return NewStoreWithOptions(dbPath, StoreOptions{})
}

// NewStoreWithOptions initializes the SQLite Store with exclusive database locking (vraxter.db.lock),
// optional full-file at-rest sealing (encrypt_db_at_rest), and column-level AES-256-GCM encryption
// backed by db_master.key.
func NewStoreWithOptions(dbPath string, opts StoreOptions) (*Store, error) {
	var fileLock *dbFileLock
	if dbPath != "" && dbPath != ":memory:" {
		var lockErr error
		fileLock, lockErr = acquireDBFileLock(dbPath)
		if lockErr != nil {
			return nil, lockErr
		}
	}
	releaseOnErr := func() {
		if fileLock != nil {
			_ = fileLock.Release()
		}
	}

	crypto := opts.Crypto
	if crypto == nil {
		var err error
		if strings.TrimSpace(opts.MasterKeyPath) != "" {
			crypto, err = security.NewCryptoService(opts.MasterKeyPath)
		} else if dbPath != "" && dbPath != ":memory:" {
			crypto, err = security.NewDBMasterCryptoService(filepath.Dir(dbPath))
		} else {
			crypto, err = security.NewDBMasterCryptoService("")
		}
		if err != nil {
			releaseOnErr()
			return nil, fmt.Errorf("failed initializing db_master.key crypto service: %w", err)
		}
	}

	workingPath := dbPath
	sealedEncPath := ""
	encryptAtRest := opts.EncryptAtRest

	if dbPath != "" && dbPath != ":memory:" {
		sealedEncPath = dbPath + ".enc"
		// Automatically enable sealed mode if a sealed .db.enc archive exists on disk
		if !encryptAtRest {
			if _, errEnc := os.Stat(sealedEncPath); errEnc == nil {
				if _, errPlain := os.Stat(dbPath); os.IsNotExist(errPlain) {
					encryptAtRest = true
				}
			}
		}

		if encryptAtRest {
			workingPath = filepath.Join(filepath.Dir(dbPath), "."+filepath.Base(dbPath)+".working")
			_ = os.Remove(workingPath)
			_ = os.Remove(workingPath + "-wal")
			_ = os.Remove(workingPath + "-shm")

			if infoEnc, err := os.Stat(sealedEncPath); err == nil && infoEnc.Size() > 0 {
				if err := crypto.DecryptFile(sealedEncPath, workingPath); err != nil {
					releaseOnErr()
					return nil, fmt.Errorf("failed decrypting sealed SQLite database %s: %w", sealedEncPath, err)
				}
				_ = os.Remove(dbPath)
				_ = os.Remove(dbPath + "-wal")
				_ = os.Remove(dbPath + "-shm")
			} else if infoPlain, err := os.Stat(dbPath); err == nil && infoPlain.Size() > 0 {
				raw, readErr := os.ReadFile(dbPath)
				if readErr != nil {
					releaseOnErr()
					return nil, fmt.Errorf("failed reading existing SQLite file %s: %w", dbPath, readErr)
				}
				if bytes.HasPrefix(raw, []byte(security.EncryptedBlobMagic)) {
					plain, decErr := crypto.DecryptBytes(raw)
					if decErr != nil {
						releaseOnErr()
						return nil, fmt.Errorf("failed decrypting sealed SQLite file %s: %w", dbPath, decErr)
					}
					if err := utils.AtomicWriteFile(workingPath, plain, 0600); err != nil {
						releaseOnErr()
						return nil, err
					}
				} else {
					if err := utils.AtomicWriteFile(workingPath, raw, 0600); err != nil {
						releaseOnErr()
						return nil, err
					}
				}
				_ = os.Remove(dbPath)
				_ = os.Remove(dbPath + "-wal")
				_ = os.Remove(dbPath + "-shm")
			}
		}
	}

	existedBefore := false
	if workingPath != ":memory:" && workingPath != "" {
		if info, err := os.Stat(workingPath); err == nil && info.Size() > 0 {
			existedBefore = true
		}
	}

	db, err := sql.Open("sqlite", workingPath)
	if err != nil {
		releaseOnErr()
		return nil, fmt.Errorf("could not open db: %w", err)
	}

	// Pin to a single connection BEFORE applying connection-scoped PRAGMAs (foreign_keys, busy_timeout, synchronous)
	// so every query inherits the exact durability and referential integrity configuration.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		_ = db.Close()
		releaseOnErr()
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
			releaseOnErr()
			return nil, fmt.Errorf("could not set pragma %s: %w", p, err)
		}
	}

	// Secure-by-Design: Perform transactionally consistent WAL-aware backup via VACUUM INTO
	if existedBefore {
		if !encryptAtRest {
			if err := autoBackup(db, workingPath); err != nil {
				log.Printf("Warning: SQLite VACUUM INTO autoBackup failed: %v", err)
			}
		}
		if crypto != nil && dbPath != "" && dbPath != ":memory:" {
			if _, err := autoBackupEncrypted(db, dbPath, crypto); err != nil {
				log.Printf("Warning: SQLite encrypted autoBackup failed: %v", err)
			}
		}
	}

	store := &Store{
		Conn:          db,
		dbPath:        dbPath,
		workingPath:   workingPath,
		sealedEncPath: sealedEncPath,
		encryptAtRest: encryptAtRest,
		crypto:        crypto,
		fileLock:      fileLock,
	}

	// Bootstrap required tables
	if err := store.initSchema(); err != nil {
		_ = db.Close()
		releaseOnErr()
		return nil, fmt.Errorf("failed to init db schema: %w", err)
	}

	if workingPath != ":memory:" && workingPath != "" {
		_ = os.Chmod(workingPath, 0600)
	}

	return store, nil
}

// Crypto returns the store's AES-256-GCM CryptoService.
func (s *Store) Crypto() *security.CryptoService {
	if s == nil {
		return nil
	}
	return s.crypto
}

// SetCrypto overrides the store's AES-256-GCM CryptoService.
func (s *Store) SetCrypto(c *security.CryptoService) {
	if s != nil {
		s.crypto = c
	}
}

// EncryptField encrypts a sensitive column string with AES-256-GCM ("vraxter-enc-v1:...").
// It fails closed and returns an error if the encryption service is unavailable or fails,
// never silently falling back to plaintext storage.
func (s *Store) EncryptField(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	if s == nil || s.crypto == nil {
		return "", ErrEncryptionUnavailable
	}
	enc, err := s.crypto.EncryptEnvelope(plain)
	if err != nil {
		return "", fmt.Errorf("failed encrypting database field: %w", err)
	}
	return enc, nil
}

// DecryptField decrypts an AES-256-GCM "vraxter-enc-v1:..." column string (or returns legacy plaintext).
func (s *Store) DecryptField(val string) string {
	if s == nil || s.crypto == nil || val == "" {
		return val
	}
	dec, err := s.crypto.DecryptEnvelope(val)
	if err != nil {
		return val
	}
	return dec
}

// EncryptBlob encrypts a sensitive binary column with AES-256-GCM ("VRXENC01" header).
// It fails closed and returns an error if the encryption service is unavailable or fails,
// never silently falling back to plaintext storage.
func (s *Store) EncryptBlob(plain []byte) ([]byte, error) {
	if len(plain) == 0 {
		return plain, nil
	}
	if s == nil || s.crypto == nil {
		return nil, ErrEncryptionUnavailable
	}
	enc, err := s.crypto.EncryptBytes(plain)
	if err != nil {
		return nil, fmt.Errorf("failed encrypting database blob: %w", err)
	}
	return enc, nil
}

// DecryptBlob decrypts an AES-256-GCM "VRXENC01" binary column (or returns legacy blob).
func (s *Store) DecryptBlob(val []byte) []byte {
	if s == nil || s.crypto == nil || len(val) == 0 {
		return val
	}
	dec, err := s.crypto.DecryptBytes(val)
	if err != nil {
		return val
	}
	return dec
}

// BackupEncrypted creates an AES-256-GCM encrypted snapshot (.db.enc, mode 0600) of the live database.
func (s *Store) BackupEncrypted() (string, error) {
	if s == nil || s.Conn == nil || s.crypto == nil {
		return "", fmt.Errorf("store or crypto service is not initialized")
	}
	targetBase := s.dbPath
	if targetBase == "" || targetBase == ":memory:" {
		return "", fmt.Errorf("cannot create file backup for in-memory database")
	}
	return autoBackupEncrypted(s.Conn, targetBase, s.crypto)
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

	CREATE TABLE IF NOT EXISTS audit_ledger (
		seq INTEGER PRIMARY KEY AUTOINCREMENT,
		entry_id TEXT UNIQUE NOT NULL,
		timestamp_utc TEXT NOT NULL,
		trace_id TEXT NOT NULL,
		session_id TEXT NOT NULL DEFAULT '',
		actor TEXT NOT NULL,
		event_type TEXT NOT NULL,
		payload_json TEXT NOT NULL,
		prev_hash TEXT NOT NULL,
		entry_hash TEXT NOT NULL,
		signer_key_id TEXT NOT NULL,
		signature TEXT NOT NULL
	);
	CREATE INDEX IF NOT EXISTS idx_audit_ledger_trace ON audit_ledger (trace_id);
	CREATE INDEX IF NOT EXISTS idx_audit_ledger_ts ON audit_ledger (timestamp_utc);

	CREATE TRIGGER IF NOT EXISTS trg_audit_ledger_no_update
	BEFORE UPDATE ON audit_ledger
	BEGIN
		SELECT RAISE(ABORT, 'audit_ledger is append-only');
	END;

	CREATE TRIGGER IF NOT EXISTS trg_audit_ledger_no_delete
	BEFORE DELETE ON audit_ledger
	BEGIN
		SELECT RAISE(ABORT, 'audit_ledger is append-only');
	END;

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
	if s == nil {
		return nil
	}
	defer func() {
		if s.fileLock != nil {
			_ = s.fileLock.Release()
			s.fileLock = nil
		}
	}()
	if s.Conn == nil {
		return nil
	}
	conn := s.Conn
	s.Conn = nil
	if s.encryptAtRest && s.workingPath != "" && s.workingPath != ":memory:" && s.crypto != nil {
		snapTmp := s.workingPath + ".seal.tmp"
		_ = os.Remove(snapTmp)
		_, vacErr := conn.Exec("VACUUM INTO ?", snapTmp)
		closeErr := conn.Close()

		srcToEncrypt := snapTmp
		if vacErr != nil {
			srcToEncrypt = s.workingPath
		}
		encTarget := s.sealedEncPath
		if encTarget == "" {
			encTarget = s.dbPath + ".enc"
		}
		encErr := s.crypto.EncryptFile(srcToEncrypt, encTarget)
		_ = os.Remove(snapTmp)
		if encErr == nil {
			// Remove all unencrypted SQLite and WAL/SHM artifacts from disk
			for _, p := range []string{
				s.workingPath, s.workingPath + "-wal", s.workingPath + "-shm",
				s.dbPath, s.dbPath + "-wal", s.dbPath + "-shm",
			} {
				if p != "" && p != encTarget {
					_ = os.Remove(p)
				}
			}
		}
		if closeErr != nil {
			return closeErr
		}
		return encErr
	}
	return conn.Close()
}

func autoBackupEncrypted(conn *sql.DB, dbPath string, crypto *security.CryptoService) (string, error) {
	if crypto == nil {
		return "", fmt.Errorf("nil crypto service")
	}
	backupsDir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(backupsDir, 0700); err != nil {
		return "", err
	}

	timestamp := time.Now().Format("2006-01-02")
	encBackupPath := filepath.Join(backupsDir, fmt.Sprintf("vraxter_%s.db.enc", timestamp))

	tmpSnap := filepath.Join(backupsDir, fmt.Sprintf(".vraxter_%s.snap.tmp", timestamp))
	_ = os.Remove(tmpSnap)
	defer os.Remove(tmpSnap)

	if _, err := conn.Exec("VACUUM INTO ?", tmpSnap); err != nil {
		return "", fmt.Errorf("VACUUM INTO failed: %w", err)
	}

	if err := crypto.EncryptFile(tmpSnap, encBackupPath); err != nil {
		return "", fmt.Errorf("failed encrypting backup snapshot: %w", err)
	}
	_ = os.Chmod(encBackupPath, 0600)

	// Rolling retention: maintain the last 7 daily encrypted backups
	files, err := os.ReadDir(backupsDir)
	if err == nil {
		var encFiles []os.DirEntry
		for _, f := range files {
			if !f.IsDir() && strings.HasPrefix(f.Name(), "vraxter_") && strings.HasSuffix(f.Name(), ".db.enc") {
				encFiles = append(encFiles, f)
			}
		}
		if len(encFiles) > 7 {
			sort.Slice(encFiles, func(i, j int) bool {
				return encFiles[i].Name() < encFiles[j].Name()
			})
			for i := 0; i < len(encFiles)-7; i++ {
				_ = os.Remove(filepath.Join(backupsDir, encFiles[i].Name()))
			}
		}
	}

	return encBackupPath, nil
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
