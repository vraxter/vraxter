// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package audit

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/telemetry"
	"github.com/vraxter/vraxter/internal/utils"
)

const (
	// AuditDomainSeparator is the domain prefix for all V1 audit entry hashes.
	AuditDomainSeparator = "VRAXTER-AUDIT-V1"
	// GenesisPrevHash is the 64-zero prev_hash required for the genesis entry (seq=1).
	GenesisPrevHash = "0000000000000000000000000000000000000000000000000000000000000000"
	// RedactedPlaceholder replaces any detected secret or API key in audit payloads.
	RedactedPlaceholder = "[REDACTED]"
)

// Standard causal chain audit event types.
const (
	EventRequestReceived  = "REQUEST_RECEIVED"
	EventContextAssembled = "CONTEXT_ASSEMBLED"
	EventPolicyEvaluated  = "POLICY_EVALUATED"
	EventHITLDecision     = "HITL_DECISION"
	EventHostExecStart    = "HOST_EXEC_START"
	EventHostExecEnd      = "HOST_EXEC_END"
	EventWASMExecStart    = "WASM_EXEC_START"
	EventWASMExecEnd      = "WASM_EXEC_END"
	EventResponseEmitted  = "RESPONSE_EMITTED"
)

// AuditEntry represents a single immutable row in audit_ledger.
type AuditEntry struct {
	Seq             int64  `json:"seq"`
	EntryID         string `json:"entry_id"`
	TimestampUTC    string `json:"timestamp_utc"`
	TraceID         string `json:"trace_id"`
	SessionID       string `json:"session_id"`
	Actor           string `json:"actor"`
	EventType       string `json:"event_type"`
	PayloadJSON     string `json:"payload_json"`
	PrevHash        string `json:"prev_hash"`
	EntryHash       string `json:"entry_hash"`
	SignerKeyID     string `json:"signer_key_id"`
	Signature       string `json:"signature"`
	DaemonPublicKey string `json:"daemon_public_key,omitempty"`
}

// AuditEventInput contains the parameters passed when recording a new audit event.
type AuditEventInput struct {
	TraceID   string
	SessionID string
	Actor     string
	EventType string
	Payload   interface{}
}

// VerificationError pinpoints the exact sequence number where hash-chain or signature integrity failed.
type VerificationError struct {
	Seq     int64
	EntryID string
	Reason  string
}

func (e *VerificationError) Error() string {
	if e.EntryID != "" {
		return fmt.Sprintf("audit chain integrity compromised at seq=%d (entry_id=%s): %s", e.Seq, e.EntryID, e.Reason)
	}
	return fmt.Sprintf("audit chain integrity compromised at seq=%d: %s", e.Seq, e.Reason)
}

// ExportFilter controls optional filtering for vraxter audit export.
type ExportFilter struct {
	Since   time.Time
	TraceID string
}

// ExportHeader is written as the first metadata line of exported JSONL files for offline verification.
type ExportHeader struct {
	Meta            string `json:"_meta"`
	DaemonPublicKey string `json:"daemon_public_key"`
	SignerKeyID     string `json:"signer_key_id"`
	ExportedAtUTC   string `json:"exported_at_utc"`
	PartialChain    bool   `json:"partial_chain,omitempty"`
}

var (
	defaultLedgerMu sync.RWMutex
	defaultLedger   *Ledger

	secretValuePatterns = []*regexp.Regexp{
		regexp.MustCompile(`sk-[A-Za-z0-9_\-]{12,}`),
		regexp.MustCompile(`AIza[0-9A-Za-z_\-]{20,}`),
		regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9_\-\.=]{12,}`),
		regexp.MustCompile(`(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{16,}`),
		regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
		regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`),
	}
)

type auditSessionCtxKey struct{}
type auditActorCtxKey struct{}
type auditOriginCtxKey struct{}

// WithSessionID attaches a session/conversation ID to ctx for downstream audit events.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, auditSessionCtxKey{}, strings.TrimSpace(sessionID))
}

// SessionIDFromContext extracts the session/conversation ID from ctx, if present.
func SessionIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(auditSessionCtxKey{}).(string); ok {
		return v
	}
	return ""
}

// WithActor attaches an actor identifier ("user", "specialist:<name>", "system") to ctx.
func WithActor(ctx context.Context, actor string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, auditActorCtxKey{}, strings.TrimSpace(actor))
}

// ActorFromContext extracts the actor identifier from ctx, defaulting to "system".
func ActorFromContext(ctx context.Context) string {
	if ctx == nil {
		return "system"
	}
	if v, ok := ctx.Value(auditActorCtxKey{}).(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return "system"
}

// WithOrigin attaches the request origin ("grpc", "tui", "cli") to ctx.
func WithOrigin(ctx context.Context, origin string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, auditOriginCtxKey{}, strings.TrimSpace(origin))
}

// OriginFromContext extracts the request origin from ctx, defaulting to "local".
func OriginFromContext(ctx context.Context) string {
	if ctx == nil {
		return "local"
	}
	if v, ok := ctx.Value(auditOriginCtxKey{}).(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return "local"
}

// SetDefaultLedger registers the process-wide forensic audit ledger.
func SetDefaultLedger(l *Ledger) {
	defaultLedgerMu.Lock()
	defer defaultLedgerMu.Unlock()
	defaultLedger = l
}

// DefaultLedger returns the process-wide forensic audit ledger (may be nil in unit tests that do not initialize it).
func DefaultLedger() *Ledger {
	defaultLedgerMu.RLock()
	defer defaultLedgerMu.RUnlock()
	return defaultLedger
}

// Record appends an audit event to the default ledger if one is configured.
func Record(ctx context.Context, input AuditEventInput) (*AuditEntry, error) {
	l := DefaultLedger()
	if l == nil {
		return nil, nil
	}
	return l.Append(ctx, input)
}

// Ledger manages the append-only, hash-chained, Ed25519-signed forensic audit log.
type Ledger struct {
	conn        *sql.DB
	privKey     ed25519.PrivateKey
	pubKey      ed25519.PublicKey
	signerKeyID string
	jsonlPath   string
	mu          sync.Mutex
}

// EnsureSchema creates the append-only audit_ledger table and its BEFORE UPDATE / BEFORE DELETE abort triggers.
func EnsureSchema(conn *sql.DB) error {
	if conn == nil {
		return fmt.Errorf("nil database connection")
	}
	const ddl = `
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
	_, err := conn.Exec(ddl)
	return err
}

// LoadOrCreateAuditIdentity loads or atomically creates the daemon's persistent Ed25519 audit signing keypair
// in appDir (audit_identity.key with 0600 and audit_identity.pub with 0644).
func LoadOrCreateAuditIdentity(appDir string) (ed25519.PrivateKey, ed25519.PublicKey, string, error) {
	if strings.TrimSpace(appDir) == "" {
		kp, err := security.GenerateEd25519KeyPair("vraxter-daemon-audit")
		if err != nil {
			return nil, nil, "", err
		}
		return kp.PrivateKey, kp.PublicKey, kp.KeyID, nil
	}

	if err := os.MkdirAll(appDir, 0700); err != nil {
		return nil, nil, "", fmt.Errorf("failed creating audit identity directory: %w", err)
	}

	privPath := filepath.Join(appDir, "audit_identity.key")
	pubPath := filepath.Join(appDir, "audit_identity.pub")

	if priv, _, err := security.LoadEd25519PrivateKey(privPath); err == nil {
		pub := priv.Public().(ed25519.PublicKey)
		keyID := security.ComputeEd25519KeyID(pub)
		_ = os.Chmod(privPath, 0600)
		return priv, pub, keyID, nil
	}

	kp, err := security.GenerateEd25519KeyPair("vraxter-daemon-audit")
	if err != nil {
		return nil, nil, "", err
	}
	writtenPriv, writtenPub, err := security.WriteEd25519KeyPairFiles(appDir, "audit_identity", kp)
	if err != nil {
		return nil, nil, "", err
	}
	if writtenPriv != privPath {
		_ = os.Rename(writtenPriv, privPath)
	}
	if writtenPub != pubPath {
		_ = os.Rename(writtenPub, pubPath)
	}
	_ = os.Chmod(privPath, 0600)
	_ = os.Chmod(pubPath, 0644)
	return kp.PrivateKey, kp.PublicKey, kp.KeyID, nil
}

// NewLedger initializes an append-only forensic audit ledger backed by store,
// persisting the daemon's Ed25519 signing identity in appDir and optionally streaming
// entries to jsonlPath.
func NewLedger(store *db.Store, appDir string, jsonlPath string) (*Ledger, error) {
	if store == nil || store.Conn == nil {
		return nil, fmt.Errorf("audit ledger requires a valid SQLite store")
	}
	if err := EnsureSchema(store.Conn); err != nil {
		return nil, fmt.Errorf("failed initializing audit_ledger schema: %w", err)
	}

	priv, pub, keyID, err := LoadOrCreateAuditIdentity(appDir)
	if err != nil {
		return nil, fmt.Errorf("failed initializing audit signing identity: %w", err)
	}

	return &Ledger{
		conn:        store.Conn,
		privKey:     priv,
		pubKey:      pub,
		signerKeyID: keyID,
		jsonlPath:   strings.TrimSpace(jsonlPath),
	}, nil
}

// SetJSONLPath configures or updates the optional real-time JSONL sink path.
func (l *Ledger) SetJSONLPath(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.jsonlPath = strings.TrimSpace(path)
}

// PublicKey returns the daemon's Ed25519 public key used to sign audit entries.
func (l *Ledger) PublicKey() ed25519.PublicKey {
	return append(ed25519.PublicKey(nil), l.pubKey...)
}

// PublicKeyBase64 returns the base64-encoded Ed25519 public key of the daemon.
func (l *Ledger) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(l.pubKey)
}

// SignerKeyID returns the 16-char hex KeyID of the daemon's audit signing key.
func (l *Ledger) SignerKeyID() string {
	return l.signerKeyID
}

// ComputeEntryHash calculates the domain-separated SHA-256 hash for an audit ledger entry:
// SHA256("VRAXTER-AUDIT-V1\n" + seq + "\n" + timestamp_utc + "\n" + trace_id + "\n" + event_type + "\n" + prev_hash + "\n" + canonical_payload)
func ComputeEntryHash(seq int64, timestampUTC, traceID, eventType, prevHash, canonicalPayload string) string {
	preimage := fmt.Sprintf(
		"%s\n%s\n%s\n%s\n%s\n%s\n%s",
		AuditDomainSeparator,
		strconv.FormatInt(seq, 10),
		timestampUTC,
		traceID,
		eventType,
		prevHash,
		canonicalPayload,
	)
	sum := sha256.Sum256([]byte(preimage))
	return hex.EncodeToString(sum[:])
}

// SignEntryHash signs the 64-character hex entryHash with the daemon's Ed25519 private key.
func SignEntryHash(privKey ed25519.PrivateKey, entryHash string) string {
	sig := ed25519.Sign(privKey, []byte(strings.ToLower(strings.TrimSpace(entryHash))))
	return base64.StdEncoding.EncodeToString(sig)
}

// VerifyEntrySignature validates the Ed25519 signature over entryHash using pubKey.
func VerifyEntrySignature(pubKey ed25519.PublicKey, entryHash, signatureB64 string) error {
	if len(pubKey) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid Ed25519 public key length (%d)", len(pubKey))
	}
	sigBytes, err := base64.StdEncoding.DecodeString(strings.TrimSpace(signatureB64))
	if err != nil || len(sigBytes) != ed25519.SignatureSize {
		return fmt.Errorf("malformed base64 Ed25519 signature")
	}
	if !ed25519.Verify(pubKey, []byte(strings.ToLower(strings.TrimSpace(entryHash))), sigBytes) {
		return fmt.Errorf("Ed25519 signature verification failed")
	}
	return nil
}

// CanonicalizeAndRedactPayload converts arbitrary payload data into deterministic canonical JSON
// with automatic redaction of secret keys and credential patterns.
func CanonicalizeAndRedactPayload(payload interface{}) (string, error) {
	if payload == nil {
		return "{}", nil
	}

	var rawBytes []byte
	switch v := payload.(type) {
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return "{}", nil
		}
		if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			rawBytes = []byte(trimmed)
		} else {
			b, err := json.Marshal(map[string]string{"message": v})
			if err != nil {
				return "", err
			}
			rawBytes = b
		}
	case []byte:
		trimmed := bytes.TrimSpace(v)
		if len(trimmed) == 0 {
			return "{}", nil
		}
		rawBytes = trimmed
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}
		rawBytes = b
	}

	var generic interface{}
	if err := json.Unmarshal(rawBytes, &generic); err != nil {
		// Wrap non-JSON strings safely
		b, marshalErr := json.Marshal(map[string]string{"raw": redactSecretString(string(rawBytes))})
		if marshalErr != nil {
			return "", marshalErr
		}
		if err := json.Unmarshal(b, &generic); err != nil {
			return "", err
		}
	}

	redacted := redactValueRecursive("", generic)
	var buf bytes.Buffer
	if err := writeCanonicalAuditJSON(&buf, redacted); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func isSensitiveFieldKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	if k == "" {
		return false
	}
	// Explicitly safe keys that contain substrings like "key" or "token"
	switch k {
	case "key_id", "signer_key_id", "public_key", "daemon_public_key", "tokens_used", "max_tokens", "token_count":
		return false
	}
	sensitiveTokens := []string{
		"api_key", "apikey", "secret", "password", "passwd",
		"private_key", "master_key", "access_token", "refresh_token",
		"auth_token", "client_secret", "authorization", "credential",
	}
	for _, s := range sensitiveTokens {
		if strings.Contains(k, s) {
			return true
		}
	}
	if k == "token" || k == "key" || k == "bearer" {
		return true
	}
	return false
}

func redactSecretString(s string) string {
	out := s
	for _, re := range secretValuePatterns {
		out = re.ReplaceAllString(out, RedactedPlaceholder)
	}
	return out
}

func redactValueRecursive(parentKey string, v interface{}) interface{} {
	if isSensitiveFieldKey(parentKey) {
		return RedactedPlaceholder
	}
	switch val := v.(type) {
	case map[string]interface{}:
		out := make(map[string]interface{}, len(val))
		for k, child := range val {
			if isSensitiveFieldKey(k) {
				out[k] = RedactedPlaceholder
			} else {
				out[k] = redactValueRecursive(k, child)
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(val))
		for i, elem := range val {
			out[i] = redactValueRecursive("", elem)
		}
		return out
	case string:
		return redactSecretString(val)
	default:
		return val
	}
}

func writeCanonicalAuditJSON(buf *bytes.Buffer, v interface{}) error {
	switch val := v.(type) {
	case map[string]interface{}:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			kb, err := json.Marshal(k)
			if err != nil {
				return err
			}
			buf.Write(kb)
			buf.WriteByte(':')
			if err := writeCanonicalAuditJSON(buf, val[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
		return nil
	case []interface{}:
		buf.WriteByte('[')
		for i, elem := range val {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonicalAuditJSON(buf, elem); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
		return nil
	default:
		var raw bytes.Buffer
		enc := json.NewEncoder(&raw)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(val); err != nil {
			return err
		}
		buf.Write(bytes.TrimRight(raw.Bytes(), "\n"))
		return nil
	}
}

// Append records a new hash-chained, Ed25519-signed forensic event in SQLite (and optional JSONL sink).
func (l *Ledger) Append(ctx context.Context, input AuditEventInput) (*AuditEntry, error) {
	if l == nil || l.conn == nil {
		return nil, fmt.Errorf("audit ledger is not initialized")
	}

	eventType := strings.ToUpper(strings.TrimSpace(input.EventType))
	if eventType == "" {
		return nil, fmt.Errorf("audit event_type is required")
	}

	actor := strings.TrimSpace(input.Actor)
	if actor == "" {
		actor = ActorFromContext(ctx)
	}
	if actor == "" {
		actor = "system"
	}

	traceID := strings.TrimSpace(input.TraceID)
	if traceID == "" {
		traceID = telemetry.TraceIDFromContext(ctx)
	}
	if traceID == "" {
		traceID = strings.ReplaceAll(uuid.New().String(), "-", "")
	}

	sessionID := strings.TrimSpace(input.SessionID)
	if sessionID == "" {
		sessionID = SessionIDFromContext(ctx)
	}

	canonicalPayload, err := CanonicalizeAndRedactPayload(input.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed canonicalizing audit payload: %w", err)
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	tx, err := l.conn.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed starting audit transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var lastSeq int64
	var lastHash string
	row := tx.QueryRow(`SELECT seq, entry_hash FROM audit_ledger ORDER BY seq DESC LIMIT 1`)
	switch scanErr := row.Scan(&lastSeq, &lastHash); scanErr {
	case sql.ErrNoRows:
		lastSeq = 0
		lastHash = GenesisPrevHash
	case nil:
		// OK
	default:
		return nil, fmt.Errorf("failed querying previous audit_ledger entry: %w", scanErr)
	}

	nextSeq := lastSeq + 1
	prevHash := lastHash
	if nextSeq == 1 {
		prevHash = GenesisPrevHash
	}

	entryID := uuid.New().String()
	timestampUTC := time.Now().UTC().Format(time.RFC3339Nano)
	entryHash := ComputeEntryHash(nextSeq, timestampUTC, traceID, eventType, prevHash, canonicalPayload)
	signature := SignEntryHash(l.privKey, entryHash)

	_, err = tx.Exec(
		`INSERT INTO audit_ledger (seq, entry_id, timestamp_utc, trace_id, session_id, actor, event_type, payload_json, prev_hash, entry_hash, signer_key_id, signature)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		nextSeq, entryID, timestampUTC, traceID, sessionID, actor, eventType, canonicalPayload, prevHash, entryHash, l.signerKeyID, signature,
	)
	if err != nil {
		return nil, fmt.Errorf("failed inserting audit_ledger entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed committing audit_ledger entry: %w", err)
	}
	committed = true

	entry := &AuditEntry{
		Seq:             nextSeq,
		EntryID:         entryID,
		TimestampUTC:    timestampUTC,
		TraceID:         traceID,
		SessionID:       sessionID,
		Actor:           actor,
		EventType:       eventType,
		PayloadJSON:     canonicalPayload,
		PrevHash:        prevHash,
		EntryHash:       entryHash,
		SignerKeyID:     l.signerKeyID,
		Signature:       signature,
		DaemonPublicKey: l.PublicKeyBase64(),
	}

	if l.jsonlPath != "" {
		if err := appendEntryToJSONL(l.jsonlPath, entry); err != nil {
			return entry, fmt.Errorf("failed appending audit entry to JSONL sink %s: %w", l.jsonlPath, err)
		}
	}

	return entry, nil
}

func appendEntryToJSONL(path string, entry *AuditEntry) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer f.Close()

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(entry); err != nil {
		return err
	}
	if _, err := f.Write(buf.Bytes()); err != nil {
		return err
	}
	return f.Sync()
}

// ListEntries returns all entries in audit_ledger ordered by seq ASC, optionally filtered.
func (l *Ledger) ListEntries(filter ExportFilter) ([]AuditEntry, error) {
	if l == nil || l.conn == nil {
		return nil, fmt.Errorf("audit ledger is not initialized")
	}

	rows, err := l.conn.Query(`
		SELECT seq, entry_id, timestamp_utc, trace_id, session_id, actor, event_type, payload_json, prev_hash, entry_hash, signer_key_id, signature
		FROM audit_ledger
		ORDER BY seq ASC`)
	if err != nil {
		return nil, fmt.Errorf("failed querying audit_ledger: %w", err)
	}
	defer rows.Close()

	var entries []AuditEntry
	pubB64 := l.PublicKeyBase64()
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(
			&e.Seq, &e.EntryID, &e.TimestampUTC, &e.TraceID, &e.SessionID,
			&e.Actor, &e.EventType, &e.PayloadJSON, &e.PrevHash, &e.EntryHash,
			&e.SignerKeyID, &e.Signature,
		); err != nil {
			return nil, fmt.Errorf("failed scanning audit_ledger row: %w", err)
		}
		if filter.TraceID != "" && !strings.EqualFold(e.TraceID, strings.TrimSpace(filter.TraceID)) {
			continue
		}
		if !filter.Since.IsZero() {
			ts, parseErr := time.Parse(time.RFC3339Nano, e.TimestampUTC)
			if parseErr != nil {
				ts, parseErr = time.Parse(time.RFC3339, e.TimestampUTC)
			}
			if parseErr == nil && ts.Before(filter.Since) {
				continue
			}
		}
		e.DaemonPublicKey = pubB64
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return entries, nil
}

// VerifyChain validates the entire SQLite audit_ledger table sequentially from seq=1,
// checking sequence continuity, prev_hash chaining, entry_hash recalculation, and Ed25519 signatures.
func (l *Ledger) VerifyChain() (int, error) {
	entries, err := l.ListEntries(ExportFilter{})
	if err != nil {
		return 0, err
	}
	if err := VerifyEntries(entries, l.pubKey, false); err != nil {
		return 0, err
	}
	return len(entries), nil
}

// VerifyEntries verifies a slice of AuditEntry records against the expected hash-chain rules
// and Ed25519 public key. When allowPartial is false (default for VerifyChain), the slice must
// start at seq=1 with GenesisPrevHash and have strictly contiguous seq numbers (1, 2, 3, ...).
func VerifyEntries(entries []AuditEntry, defaultPubKey ed25519.PublicKey, allowPartial bool) error {
	if len(entries) == 0 {
		return nil
	}

	var expectedSeq int64 = 1
	expectedPrevHash := GenesisPrevHash

	for i, e := range entries {
		if i == 0 && allowPartial {
			expectedSeq = e.Seq
			expectedPrevHash = e.PrevHash
		}

		if e.Seq != expectedSeq {
			return &VerificationError{
				Seq:     expectedSeq,
				EntryID: e.EntryID,
				Reason:  fmt.Sprintf("sequence gap or out-of-order row: expected seq=%d, found seq=%d", expectedSeq, e.Seq),
			}
		}

		if !allowPartial || i > 0 {
			if subtle.ConstantTimeCompare([]byte(strings.ToLower(e.PrevHash)), []byte(strings.ToLower(expectedPrevHash))) != 1 {
				return &VerificationError{
					Seq:     e.Seq,
					EntryID: e.EntryID,
					Reason:  fmt.Sprintf("broken prev_hash link: expected %s, got %s", expectedPrevHash, e.PrevHash),
				}
			}
		} else if e.Seq == 1 && subtle.ConstantTimeCompare([]byte(strings.ToLower(e.PrevHash)), []byte(GenesisPrevHash)) != 1 {
			return &VerificationError{
				Seq:     1,
				EntryID: e.EntryID,
				Reason:  fmt.Sprintf("invalid genesis prev_hash: expected 64 zeros, got %s", e.PrevHash),
			}
		}

		recalculatedHash := ComputeEntryHash(e.Seq, e.TimestampUTC, e.TraceID, e.EventType, e.PrevHash, e.PayloadJSON)
		if subtle.ConstantTimeCompare([]byte(recalculatedHash), []byte(strings.ToLower(strings.TrimSpace(e.EntryHash)))) != 1 {
			return &VerificationError{
				Seq:     e.Seq,
				EntryID: e.EntryID,
				Reason:  fmt.Sprintf("entry_hash mismatch (payload or metadata tampered): expected %s, recalculated %s", e.EntryHash, recalculatedHash),
			}
		}

		// Resolve Ed25519 public key for signature verification
		pubKey := defaultPubKey
		if len(pubKey) == 0 && strings.TrimSpace(e.DaemonPublicKey) != "" {
			if raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(e.DaemonPublicKey)); err == nil && len(raw) == ed25519.PublicKeySize {
				pubKey = ed25519.PublicKey(raw)
			}
		}
		if len(pubKey) != ed25519.PublicKeySize {
			return &VerificationError{
				Seq:     e.Seq,
				EntryID: e.EntryID,
				Reason:  "missing or invalid daemon Ed25519 public key for signature verification",
			}
		}

		derivedKeyID := security.ComputeEd25519KeyID(pubKey)
		if !strings.EqualFold(strings.TrimSpace(e.SignerKeyID), derivedKeyID) {
			return &VerificationError{
				Seq:     e.Seq,
				EntryID: e.EntryID,
				Reason:  fmt.Sprintf("signer_key_id mismatch: entry has %s, daemon public key has %s", e.SignerKeyID, derivedKeyID),
			}
		}

		if err := VerifyEntrySignature(pubKey, e.EntryHash, e.Signature); err != nil {
			return &VerificationError{
				Seq:     e.Seq,
				EntryID: e.EntryID,
				Reason:  fmt.Sprintf("invalid Ed25519 signature: %v", err),
			}
		}

		expectedSeq = e.Seq + 1
		expectedPrevHash = e.EntryHash
	}

	return nil
}

// ExportJSONL exports the ledger (full or filtered by Since / TraceID) to outPath in verifiable JSONL format,
// prepending a metadata header line containing daemon_public_key.
func (l *Ledger) ExportJSONL(outPath string, filter ExportFilter) (int, error) {
	entries, err := l.ListEntries(filter)
	if err != nil {
		return 0, err
	}

	isPartial := !filter.Since.IsZero() || strings.TrimSpace(filter.TraceID) != ""
	header := ExportHeader{
		Meta:            "vraxter-audit-export/1",
		DaemonPublicKey: l.PublicKeyBase64(),
		SignerKeyID:     l.signerKeyID,
		ExportedAtUTC:   time.Now().UTC().Format(time.RFC3339Nano),
		PartialChain:    isPartial,
	}

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(header); err != nil {
		return 0, err
	}
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return 0, err
		}
	}

	if err := utils.AtomicWriteFile(outPath, buf.Bytes(), 0600); err != nil {
		return 0, err
	}
	return len(entries), nil
}

// VerifyJSONLFile reads an exported or live JSONL audit file and verifies every entry's hash chain and Ed25519 signature.
func VerifyJSONLFile(jsonlPath string, defaultPubKey ed25519.PublicKey) (int, error) {
	f, err := os.Open(jsonlPath)
	if err != nil {
		return 0, fmt.Errorf("failed opening audit JSONL file %s: %w", jsonlPath, err)
	}
	defer f.Close()

	var entries []AuditEntry
	pubKey := defaultPubKey
	scanner := bufio.NewScanner(f)
	// Allow up to 4 MB per JSONL line
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var metaProbe ExportHeader
		if err := json.Unmarshal(line, &metaProbe); err == nil && metaProbe.Meta != "" {
			if len(pubKey) == 0 && metaProbe.DaemonPublicKey != "" {
				if raw, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(metaProbe.DaemonPublicKey)); decErr == nil && len(raw) == ed25519.PublicKeySize {
					pubKey = ed25519.PublicKey(raw)
				}
			}
			continue
		}

		var entry AuditEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return 0, fmt.Errorf("malformed JSON on line %d of %s: %w", lineNum, jsonlPath, err)
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("error reading %s: %w", jsonlPath, err)
	}

	if err := VerifyEntries(entries, pubKey, false); err != nil {
		return 0, err
	}
	return len(entries), nil
}
