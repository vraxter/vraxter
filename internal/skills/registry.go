package skills

import (
	"fmt"
	"github.com/patagonicrune/vraxter/pkg/types"
)

// Registry manages the discovery, validation, and retrieval of installed skills.
type Registry struct {
	skills map[string]types.SkillManifest
}

// NewRegistry initializes a Registry.
// In the future, this should sync with the local SQLite block.
func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]types.SkillManifest),
	}
}

// FindSkill returns the loaded skill manifest by ID
func (r *Registry) FindSkill(id string) (*types.SkillManifest, error) {
	if s, exists := r.skills[id]; exists {
		return &s, nil
	}
	return nil, fmt.Errorf("skill not found: %s", id)
}

// Register loads a skill into the registry
func (r *Registry) Register(manifest types.SkillManifest) error {
	if manifest.ID == "" {
		return fmt.Errorf("skill manifest must have an ID")
	}
	r.skills[manifest.ID] = manifest
	return nil
}

// GetAll returns a slice of all installed skill manifests
func (r *Registry) GetAll() []types.SkillManifest {
	var list []types.SkillManifest
	for _, s := range r.skills {
		list = append(list, s)
	}
	return list
}
