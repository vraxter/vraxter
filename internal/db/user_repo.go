// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package db

import (
	"context"
	"database/sql"
)

// UserProfile stores non-sensitive preferences and identity data
type UserProfile struct {
	ID              string
	Name            string
	Language        string
	ThemePreference string
	Expertise       string // e.g. "Senior Go Dev"
	Interests       string // e.g. "NATS, Microservices, Fitness"
	Bio             string // Brief summary for context injection
}

type UserRepository struct {
	store *Store
}

func NewUserRepository(store *Store) *UserRepository {
	return &UserRepository{store: store}
}

func (r *UserRepository) GetDefaultUser(ctx context.Context) (*UserProfile, error) {
	const query = `SELECT id, name, language, theme_preference, expertise, interests, bio FROM users LIMIT 1`
	var u UserProfile
	err := r.store.Conn.QueryRowContext(ctx, query).Scan(
		&u.ID, &u.Name, &u.Language, &u.ThemePreference, &u.Expertise, &u.Interests, &u.Bio,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepository) GetUserByName(ctx context.Context, name string) (*UserProfile, error) {
	const query = `SELECT id, name, language, theme_preference, expertise, interests, bio FROM users WHERE name = ? LIMIT 1`
	var u UserProfile
	err := r.store.Conn.QueryRowContext(ctx, query, name).Scan(
		&u.ID, &u.Name, &u.Language, &u.ThemePreference, &u.Expertise, &u.Interests, &u.Bio,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func (r *UserRepository) UpdateUser(ctx context.Context, u *UserProfile) error {
	const query = `
		UPDATE users SET 
			name = ?, 
			language = ?, 
			theme_preference = ?, 
			expertise = ?, 
			interests = ?, 
			bio = ?
		WHERE id = ?`
	
	_, err := r.store.Conn.ExecContext(ctx, query,
		u.Name, u.Language, u.ThemePreference, u.Expertise, u.Interests, u.Bio, u.ID,
	)
	return err
}

func (r *UserRepository) CreateUser(ctx context.Context, u *UserProfile) error {
	const query = `
		INSERT INTO users (id, name, language, theme_preference, expertise, interests, bio)
		VALUES (?, ?, ?, ?, ?, ?, ?)`
	
	_, err := r.store.Conn.ExecContext(ctx, query,
		u.ID, u.Name, u.Language, u.ThemePreference, u.Expertise, u.Interests, u.Bio,
	)
	return err
}
