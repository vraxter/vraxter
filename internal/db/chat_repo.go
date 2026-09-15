// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package db

import (
	"database/sql"
	"fmt"

	"github.com/patagonicrune/vraxter/pkg/types"
)

// ChatRepository handles retrieving and saving conversation messages
type ChatRepository struct {
	store *Store
}

// NewChatRepository creates a new repo tied to the shared SQLite session
func NewChatRepository(store *Store) *ChatRepository {
	return &ChatRepository{store: store}
}

// CreateConversation setups a new chat context block
func (r *ChatRepository) CreateConversation(c *types.Conversation) error {
	var specialistID *string
	if c.SpecialistID != "" {
		specialistID = &c.SpecialistID
	}
	query := "INSERT INTO conversations (id, title, specialist_id) VALUES (?, ?, ?)"
	_, err := r.store.Conn.Exec(query, c.ID, c.Title, specialistID)
	return err
}

// FindConversation finds a conversation by its exact session ID
func (r *ChatRepository) FindConversation(id string) (*types.Conversation, error) {
	query := "SELECT id, title, summary, specialist_id, created_at, updated_at FROM conversations WHERE id = ?"
	row := r.store.Conn.QueryRow(query, id)

	var c types.Conversation
	var sID, sum sql.NullString
	err := row.Scan(&c.ID, &c.Title, &sum, &sID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed fetching conversation: %w", err)
	}

	if sum.Valid {
		c.Summary = sum.String
	}
	if sID.Valid {
		c.SpecialistID = sID.String
	}
	return &c, nil
}

// FindLatestConversationBySpecialist finds the master rolling-chat for a specialist
func (r *ChatRepository) FindLatestConversationBySpecialist(specialistID string) (*types.Conversation, error) {
	query := "SELECT id, title, summary, specialist_id, created_at, updated_at FROM conversations WHERE specialist_id = ? ORDER BY created_at DESC LIMIT 1"
	row := r.store.Conn.QueryRow(query, specialistID)

	var c types.Conversation
	var sID, sum sql.NullString
	err := row.Scan(&c.ID, &c.Title, &sum, &sID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found, that's fine
		}
		return nil, fmt.Errorf("failed fetching specialist chat: %w", err)
	}

	if sID.Valid {
		c.SpecialistID = sID.String
	}
	if sum.Valid {
		c.Summary = sum.String
	}
	return &c, nil
}

// SaveMessage writes a single conversational turn attached to a specific Conversation ID
func (r *ChatRepository) SaveMessage(m *types.Message) error {
	query := "INSERT INTO messages (id, conversation_id, role, content, tokens_used) VALUES (?, ?, ?, ?, ?)"
	_, err := r.store.Conn.Exec(query, m.ID, m.ConversationID, m.Role, m.Content, m.TokensUsed)
	return err
}

// GetMessagesByConversation pulls chat history sequentially with a Rolling Window limit
func (r *ChatRepository) GetMessagesByConversation(conversationID string, limit int) ([]types.Message, error) {
	// Subquery to get the last N messages, ordered correctly
	query := `
		SELECT * FROM (
			SELECT id, conversation_id, role, content, tokens_used, timestamp 
			FROM messages 
			WHERE conversation_id = ? 
			ORDER BY timestamp DESC 
			LIMIT ?
		) sub
		ORDER BY timestamp ASC
	`
	rows, err := r.store.Conn.Query(query, conversationID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch chat history: %v", err)
	}
	defer rows.Close()

	var msgs []types.Message
	for rows.Next() {
		var m types.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.TokensUsed, &m.Timestamp); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}

// UpdateConversationSummary overwrites the background memory block
func (r *ChatRepository) UpdateConversationSummary(conversationID string, summary string) error {
	query := "UPDATE conversations SET summary = ?, updated_at = CURRENT_TIMESTAMP WHERE id = ?"
	_, err := r.store.Conn.Exec(query, summary, conversationID)
	return err
}

// TruncateOldMessages deletes messages for a conversation EXCLUDING the N most recent ones
func (r *ChatRepository) TruncateOldMessages(conversationID string, keepN int) error {
	query := `
		DELETE FROM messages 
		WHERE conversation_id = ? 
		AND id NOT IN (
			SELECT id FROM messages WHERE conversation_id = ? ORDER BY timestamp DESC LIMIT ?
		)
	`
	_, err := r.store.Conn.Exec(query, conversationID, conversationID, keepN)
	return err
}

// ListConversations returns the N most recent conversations ordered by last activity
func (r *ChatRepository) ListConversations(limit int) ([]types.Conversation, error) {
	query := `
		SELECT c.id, c.title, COALESCE(c.summary, ''), COALESCE(c.specialist_id, ''), c.created_at, c.updated_at,
			(SELECT COUNT(*) FROM messages WHERE conversation_id = c.id) as msg_count
		FROM conversations c
		ORDER BY c.updated_at DESC
		LIMIT ?
	`
	rows, err := r.store.Conn.Query(query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}
	defer rows.Close()

	var convs []types.Conversation
	for rows.Next() {
		var c types.Conversation
		var msgCount int
		if err := rows.Scan(&c.ID, &c.Title, &c.Summary, &c.SpecialistID, &c.CreatedAt, &c.UpdatedAt, &msgCount); err != nil {
			return nil, err
		}
		c.MessageCount = msgCount
		convs = append(convs, c)
	}
	return convs, nil
}

// GetFullConversation retrieves ALL messages for a conversation, ordered chronologically
func (r *ChatRepository) GetFullConversation(conversationID string) ([]types.Message, error) {
	query := `
		SELECT id, conversation_id, role, content, tokens_used, timestamp
		FROM messages
		WHERE conversation_id = ?
		ORDER BY timestamp ASC
	`
	rows, err := r.store.Conn.Query(query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch full conversation: %w", err)
	}
	defer rows.Close()

	var msgs []types.Message
	for rows.Next() {
		var m types.Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.TokensUsed, &m.Timestamp); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	return msgs, nil
}
