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
	"time"

	"github.com/vraxter/vraxter/internal/security"
)

type Provider struct {
	ID        string
	Name      string
	Type      string
	APIKey    string
	BaseURL   string
	IsActive  bool
	CreatedAt time.Time
}

type ProviderRepository struct {
	store  *Store
	crypto *security.CryptoService
}

func NewProviderRepository(store *Store, crypto *security.CryptoService) *ProviderRepository {
	return &ProviderRepository{store: store, crypto: crypto}
}

func (r *ProviderRepository) Create(p *Provider) error {
	encryptedKey, err := r.crypto.Encrypt(p.APIKey)
	if err != nil {
		return err
	}

	query := `INSERT INTO providers (id, name, type, api_key, base_url, is_active) VALUES (?, ?, ?, ?, ?, ?)`
	_, err = r.store.Conn.Exec(query, p.ID, p.Name, p.Type, encryptedKey, p.BaseURL, p.IsActive)
	return err
}

func (r *ProviderRepository) GetAll() ([]Provider, error) {
	rows, err := r.store.Conn.Query("SELECT id, name, type, api_key, base_url, is_active, created_at FROM providers ORDER BY name ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var providers []Provider
	for rows.Next() {
		var p Provider
		var encryptedKey string
		if err := rows.Scan(&p.ID, &p.Name, &p.Type, &encryptedKey, &p.BaseURL, &p.IsActive, &p.CreatedAt); err != nil {
			continue
		}
		p.APIKey, _ = r.crypto.Decrypt(encryptedKey)
		providers = append(providers, p)
	}
	return providers, nil
}

func (r *ProviderRepository) GetByIDOrName(idOrName string) (*Provider, error) {
	query := `SELECT id, name, type, api_key, base_url, is_active, created_at FROM providers WHERE id = ? OR name = ? COLLATE NOCASE LIMIT 1`
	var p Provider
	var encryptedKey string
	err := r.store.Conn.QueryRow(query, idOrName, idOrName).Scan(&p.ID, &p.Name, &p.Type, &encryptedKey, &p.BaseURL, &p.IsActive, &p.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("provider '%s' not found", idOrName)
		}
		return nil, err
	}
	p.APIKey, _ = r.crypto.Decrypt(encryptedKey)
	return &p, nil
}

func (r *ProviderRepository) Update(p *Provider) error {
	encryptedKey, err := r.crypto.Encrypt(p.APIKey)
	if err != nil {
		return err
	}

	query := `UPDATE providers SET name = ?, type = ?, api_key = ?, base_url = ?, is_active = ? WHERE id = ?`
	_, err = r.store.Conn.Exec(query, p.Name, p.Type, encryptedKey, p.BaseURL, p.IsActive, p.ID)
	return err
}

func (r *ProviderRepository) Delete(id string) error {
	_, err := r.store.Conn.Exec("DELETE FROM providers WHERE id = ?", id)
	return err
}

// RenameProviderType updates all providers and models of an old type to a new type
func RenameProviderType(store *Store, oldType, newType string) error {
	tx, err := store.Conn.Begin()
	if err != nil {
		return err
	}

	// Update providers
	_, err = tx.Exec("UPDATE providers SET type = ? WHERE type = ?", newType, oldType)
	if err != nil {
		tx.Rollback()
		return err
	}

	return tx.Commit()
}
