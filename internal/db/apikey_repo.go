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
)

type APIKey struct {
	ID        string
	Name      string
	KeyHash   string
	Scopes    string // comma separated or JSON array
	CreatedAt string
}

type APIKeyRepository struct {
	store *Store
}

func NewAPIKeyRepository(store *Store) *APIKeyRepository {
	return &APIKeyRepository{store: store}
}

func (r *APIKeyRepository) Create(key *APIKey) error {
	query := `INSERT INTO api_keys (id, name, key_hash, scopes) VALUES (?, ?, ?, ?)`
	_, err := r.store.Conn.Exec(query, key.ID, key.Name, key.KeyHash, key.Scopes)
	if err != nil {
		return fmt.Errorf("failed to create api key: %w", err)
	}
	return nil
}

func (r *APIKeyRepository) GetByKeyHash(hash string) (*APIKey, error) {
	query := `SELECT id, name, key_hash, scopes, created_at FROM api_keys WHERE key_hash = ?`
	row := r.store.Conn.QueryRow(query, hash)

	var k APIKey
	err := row.Scan(&k.ID, &k.Name, &k.KeyHash, &k.Scopes, &k.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("api key not found")
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get api key: %w", err)
	}
	return &k, nil
}

func (r *APIKeyRepository) List() ([]APIKey, error) {
	query := `SELECT id, name, key_hash, scopes, created_at FROM api_keys ORDER BY created_at DESC`
	rows, err := r.store.Conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list api keys: %w", err)
	}
	defer rows.Close()

	var keys []APIKey
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.KeyHash, &k.Scopes, &k.CreatedAt); err != nil {
			return nil, err
		}
		keys = append(keys, k)
	}
	return keys, nil
}

func (r *APIKeyRepository) Delete(id string) error {
	query := `DELETE FROM api_keys WHERE id = ?`
	_, err := r.store.Conn.Exec(query, id)
	return err
}
