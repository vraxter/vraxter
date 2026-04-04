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

// FindLatestConversationBySpecialist finds the master rolling-chat for a specialist
func (r *ChatRepository) FindLatestConversationBySpecialist(specialistID string) (*types.Conversation, error) {
	query := "SELECT id, title, specialist_id, created_at, updated_at FROM conversations WHERE specialist_id = ? ORDER BY created_at DESC LIMIT 1"
	row := r.store.Conn.QueryRow(query, specialistID)

	var c types.Conversation
	var sID sql.NullString
	err := row.Scan(&c.ID, &c.Title, &sID, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found, that's fine
		}
		return nil, fmt.Errorf("failed fetching specialist chat: %w", err)
	}

	if sID.Valid {
		c.SpecialistID = sID.String
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
