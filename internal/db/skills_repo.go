// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package db

import (
	"encoding/json"
	"log"
	"math"

	"github.com/vraxter/vraxter/pkg/types"
)

// SkillRepository manages persistence for AI Tools and Skills
type SkillRepository struct {
	store *Store
}

func NewSkillRepository(store *Store) *SkillRepository {
	return &SkillRepository{store: store}
}

// GetAllSkills fetches all installed skills from the local DB
func (r *SkillRepository) GetAllSkills() ([]types.SkillManifest, error) {
	query := `SELECT id, name, description, version, command, language, engine, tier, score, is_official, checksum, permissions, downloads, 
	                 keywords, examples, tags, param_regex, params_schema, vector
	          FROM skills ORDER BY name ASC`
	rows, err := r.store.Conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var skills []types.SkillManifest
	for rows.Next() {
		var s types.SkillManifest
		var permissionsJSON, keywordsJSON, examplesJSON, tagsJSON string
		var vectorBlob []byte

		err := rows.Scan(
			&s.ID, &s.Name, &s.Description, &s.Version, &s.Command, &s.Language, &s.Engine, &s.Tier, &s.Score, &s.IsOfficial, &s.Checksum, &permissionsJSON, &s.Downloads,
			&keywordsJSON, &examplesJSON, &tagsJSON, &s.ParamRegex, &s.ParamsSchema, &vectorBlob,
		)
		if err != nil {
			log.Printf("DB: Failed to scan skill: %v", err)
			continue
		}

		_ = json.Unmarshal([]byte(permissionsJSON), &s.Permissions)
		_ = json.Unmarshal([]byte(keywordsJSON), &s.Keywords)
		_ = json.Unmarshal([]byte(examplesJSON), &s.Examples)
		_ = json.Unmarshal([]byte(tagsJSON), &s.Tags)

		if len(vectorBlob) > 0 {
			s.Vector = bytesToFloats(vectorBlob)
		}

		skills = append(skills, s)
	}
	return skills, nil
}

// UpsertSkill saves or updates a skill manifest
func (r *SkillRepository) UpsertSkill(s types.SkillManifest) error {
	permissionsJSON, _ := json.Marshal(s.Permissions)
	keywordsJSON, _ := json.Marshal(s.Keywords)
	examplesJSON, _ := json.Marshal(s.Examples)
	tagsJSON, _ := json.Marshal(s.Tags)
	vectorBlob := floatsToBytes(s.Vector)

	query := `INSERT INTO skills (id, name, description, version, command, language, engine, tier, score, is_official, checksum, permissions, downloads, 
	                             keywords, examples, tags, param_regex, params_schema, vector)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(id) DO UPDATE SET
	          name=excluded.name, description=excluded.description, version=excluded.version, 
	          command=excluded.command, language=excluded.language, engine=excluded.engine, 
	          tier=excluded.tier, score=excluded.score, is_official=excluded.is_official, 
	          checksum=excluded.checksum, permissions=excluded.permissions, downloads=excluded.downloads,
	          keywords=excluded.keywords, examples=excluded.examples, tags=excluded.tags, 
	          param_regex=excluded.param_regex, params_schema=excluded.params_schema, vector=excluded.vector`

	_, err := r.store.Conn.Exec(query, 
		s.ID, s.Name, s.Description, s.Version, s.Command, s.Language, s.Engine, s.Tier, s.Score, s.IsOfficial, s.Checksum, string(permissionsJSON), s.Downloads,
		string(keywordsJSON), string(examplesJSON), string(tagsJSON), s.ParamRegex, s.ParamsSchema, vectorBlob,
	)
	return err
}

// Helper functions for binary float storage
func floatsToBytes(floats []float32) []byte {
	if len(floats) == 0 {
		return nil
	}
	// We use a simple JSON encoding or binary.Write. Binary is more efficient for vectors.
	// For simplicity and to match the vector memory style of Vraxter, 1 float32 = 4 bytes.
	buf := make([]byte, len(floats)*4)
	for i, f := range floats {
		u := math.Float32bits(f)
		buf[i*4] = byte(u)
		buf[i*4+1] = byte(u >> 8)
		buf[i*4+2] = byte(u >> 16)
		buf[i*4+3] = byte(u >> 24)
	}
	return buf
}

func bytesToFloats(b []byte) []float32 {
	if len(b) == 0 {
		return nil
	}
	count := len(b) / 4
	floats := make([]float32, count)
	for i := 0; i < count; i++ {
		u := uint32(b[i*4]) | uint32(b[i*4+1])<<8 | uint32(b[i*4+2])<<16 | uint32(b[i*4+3])<<24
		floats[i] = math.Float32frombits(u)
	}
	return floats
}

func (r *SkillRepository) FindSkill(id string) (*types.SkillManifest, error) {
	query := `SELECT id, name, description, version, command, language, engine, tier, score, is_official, checksum, permissions, downloads, 
	                 keywords, examples, tags, param_regex, params_schema, vector
	          FROM skills WHERE id = ?`
	row := r.store.Conn.QueryRow(query, id)

	var s types.SkillManifest
	var permissionsJSON, keywordsJSON, examplesJSON, tagsJSON string
	var vectorBlob []byte

	err := row.Scan(
		&s.ID, &s.Name, &s.Description, &s.Version, &s.Command, &s.Language, &s.Engine, &s.Tier, &s.Score, &s.IsOfficial, &s.Checksum, &permissionsJSON, &s.Downloads,
		&keywordsJSON, &examplesJSON, &tagsJSON, &s.ParamRegex, &s.ParamsSchema, &vectorBlob,
	)
	if err != nil {
		return nil, err
	}

	_ = json.Unmarshal([]byte(permissionsJSON), &s.Permissions)
	_ = json.Unmarshal([]byte(keywordsJSON), &s.Keywords)
	_ = json.Unmarshal([]byte(examplesJSON), &s.Examples)
	_ = json.Unmarshal([]byte(tagsJSON), &s.Tags)

	if len(vectorBlob) > 0 {
		s.Vector = bytesToFloats(vectorBlob)
	}

	return &s, nil
}

func (r *SkillRepository) DeleteSkill(id string) error {
	query := `DELETE FROM skills WHERE id = ?`
	_, err := r.store.Conn.Exec(query, id)
	return err
}
