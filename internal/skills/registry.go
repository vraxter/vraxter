// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"fmt"
	"sync"

	"github.com/vraxter/vraxter/pkg/types"
)

type Registry struct {
	mu     sync.RWMutex
	skills map[string]types.SkillManifest
}

func NewRegistry() *Registry {
	return &Registry{
		skills: make(map[string]types.SkillManifest),
	}
}

func (r *Registry) FindSkill(id string) (*types.SkillManifest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if s, exists := r.skills[id]; exists {
		return &s, nil
	}
	return nil, fmt.Errorf("skill not found: %s", id)
}

func (r *Registry) Register(manifest types.SkillManifest) error {
	if manifest.ID == "" {
		return fmt.Errorf("skill manifest must have an ID")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skills[manifest.ID] = manifest
	return nil
}

func (r *Registry) GetAll() []types.SkillManifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []types.SkillManifest
	for _, s := range r.skills {
		list = append(list, s)
	}
	return list
}
