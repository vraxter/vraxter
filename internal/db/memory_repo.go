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
	"encoding/binary"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"
)

type MemoryNode struct {
	ID             string
	ConversationID string
	TextContent    string
	Vector         []float32
	CreatedAt      time.Time
}

type MemoryRepository struct {
	Store *Store
}

func NewMemoryRepository(store *Store) *MemoryRepository {
	return &MemoryRepository{Store: store}
}

// ConvertFloat32ArrayToBytes serializes floats to bytes for BLOB storage natively
func ConvertFloat32ArrayToBytes(floats []float32) ([]byte, error) {
	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.LittleEndian, floats)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ConvertBytesToFloat32Array parses BLOBs back into float arrays natively
func ConvertBytesToFloat32Array(b []byte) ([]float32, error) {
	if len(b)%4 != 0 {
		return nil, fmt.Errorf("byte slice length is not a multiple of 4")
	}
	floats := make([]float32, len(b)/4)
	buf := bytes.NewReader(b)
	err := binary.Read(buf, binary.LittleEndian, &floats)
	if err != nil {
		return nil, err
	}
	return floats, nil
}

// SaveEmbedding stores a generic intent/response pairing vector into the SQLite memory layer
func (r *MemoryRepository) SaveEmbedding(conversationID, text string, vector []float32) error {
	blob, err := ConvertFloat32ArrayToBytes(vector)
	if err != nil {
		return fmt.Errorf("failed to encode vector: %w", err)
	}

	id := uuid.New().String()
	_, err = r.Store.Conn.Exec(
		"INSERT INTO embeddings (id, conversation_id, text_content, vector) VALUES (?, ?, ?, ?)",
		id, conversationID, text, blob,
	)
	return err
}

type SearchResult struct {
	Node  MemoryNode
	Score float32
}

// SearchSimilar fetches session-specific embeddings
func (r *MemoryRepository) SearchSimilar(conversationID string, queryVector []float32, limit int) ([]SearchResult, error) {
	return r.search(conversationID, queryVector, limit)
}

// SearchGlobalSimilar fetches embeddings across ALL conversations for long-term RAG
func (r *MemoryRepository) SearchGlobalSimilar(queryVector []float32, limit int) ([]SearchResult, error) {
	return r.search("", queryVector, limit)
}

func (r *MemoryRepository) search(conversationID string, queryVector []float32, limit int) ([]SearchResult, error) {
	var query string
	var args []interface{}

	if conversationID != "" {
		query = "SELECT id, text_content, vector, created_at FROM embeddings WHERE conversation_id = ? ORDER BY created_at DESC LIMIT 1000"
		args = append(args, conversationID)
	} else {
		query = "SELECT id, text_content, vector, created_at FROM embeddings ORDER BY created_at DESC LIMIT 2000"
	}

	rows, err := r.Store.Conn.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var node MemoryNode
		var blob []byte
		if err := rows.Scan(&node.ID, &node.TextContent, &blob, &node.CreatedAt); err != nil {
			continue
		}
		
		node.Vector, err = ConvertBytesToFloat32Array(blob)
		if err != nil {
			continue
		}

		score := CosineSimilarity(queryVector, node.Vector)
		// Multi-session relevance threshold is slightly higher to avoid noise
		if score > 0.65 {
			results = append(results, SearchResult{Node: node, Score: score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if len(results) > limit {
		results = results[:limit]
	}

	return results, nil
}

func CosineSimilarity(a, b []float32) float32 {
	if len(a) != len(b) {
		return 0.0
	}
	var dot, normA, normB float32
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0.0
	}
	return dot / (float32(math.Sqrt(float64(normA))) * float32(math.Sqrt(float64(normB))))
}
