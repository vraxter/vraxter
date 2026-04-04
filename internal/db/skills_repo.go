package db

import (
	"encoding/json"
	"log"

	"github.com/patagonicrune/vraxter/pkg/types"
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
	query := `SELECT id, name, description, version, command, language, engine, tier, score, is_official, checksum, permissions, downloads 
	          FROM skills ORDER BY name ASC`
	rows, err := r.store.Conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var skills []types.SkillManifest
	for rows.Next() {
		var s types.SkillManifest
		var permissionsJSON string
		err := rows.Scan(&s.ID, &s.Name, &s.Description, &s.Version, &s.Command, &s.Language, &s.Engine, &s.Tier, &s.Score, &s.IsOfficial, &s.Checksum, &permissionsJSON, &s.Downloads)
		if err != nil {
			log.Printf("DB: Failed to scan skill: %v", err)
			continue
		}
		json.Unmarshal([]byte(permissionsJSON), &s.Permissions)
		skills = append(skills, s)
	}
	return skills, nil
}

// UpsertSkill saves or updates a skill manifest
func (r *SkillRepository) UpsertSkill(s types.SkillManifest) error {
	permissionsJSON, _ := json.Marshal(s.Permissions)
	query := `INSERT INTO skills (id, name, description, version, command, language, engine, tier, score, is_official, checksum, permissions, downloads)
	          VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	          ON CONFLICT(id) DO UPDATE SET
	          name=excluded.name, description=excluded.description, version=excluded.version, 
	          command=excluded.command, language=excluded.language, engine=excluded.engine, 
	          tier=excluded.tier, score=excluded.score, is_official=excluded.is_official, 
	          checksum=excluded.checksum, permissions=excluded.permissions, downloads=excluded.downloads`

	_, err := r.store.Conn.Exec(query, s.ID, s.Name, s.Description, s.Version, s.Command, s.Language, s.Engine, s.Tier, s.Score, s.IsOfficial, s.Checksum, string(permissionsJSON), s.Downloads)
	return err
}
