package db

import (
	"fmt"
	"time"

	"github.com/patagonicrune/vraxter/pkg/types"
)

type SpecialistRepository struct {
	Store *Store
}

func NewSpecialistRepository(store *Store) *SpecialistRepository {
	return &SpecialistRepository{Store: store}
}

func (r *SpecialistRepository) CreateSpecialist(s types.Specialist) error {
	query := `INSERT INTO specialists (id, name, expertise, model_id, system_prompt, created_at)
			  VALUES (?, ?, ?, ?, ?, ?)`
	
	if s.CreatedAt.IsZero() {
		s.CreatedAt = time.Now()
	}

	_, err := r.Store.Conn.Exec(query, s.ID, s.Name, s.Expertise, s.ModelID, s.SystemPrompt, s.CreatedAt)
	return err
}

func (r *SpecialistRepository) GetSpecialist(id string) (*types.Specialist, error) {
	query := `SELECT id, name, expertise, model_id, system_prompt, created_at FROM specialists WHERE id = ?`
	row := r.Store.Conn.QueryRow(query, id)

	var s types.Specialist
	err := row.Scan(&s.ID, &s.Name, &s.Expertise, &s.ModelID, &s.SystemPrompt, &s.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("specialist not found: %w", err)
	}

	return &s, nil
}

func (r *SpecialistRepository) GetAllSpecialists() ([]types.Specialist, error) {
	query := `SELECT id, name, expertise, model_id, system_prompt, created_at FROM specialists`
	rows, err := r.Store.Conn.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var list []types.Specialist
	for rows.Next() {
		var s types.Specialist
		if err := rows.Scan(&s.ID, &s.Name, &s.Expertise, &s.ModelID, &s.SystemPrompt, &s.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, s)
	}

	return list, nil
}

func (r *SpecialistRepository) DeleteSpecialist(id string) error {
	query := `DELETE FROM specialists WHERE id = ?`
	_, err := r.Store.Conn.Exec(query, id)
	return err
}
