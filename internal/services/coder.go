// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package services

import (
	"fmt"
	"sync"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/skills/coders"
	"github.com/vraxter/vraxter/pkg/interfaces"
)

// CoderService is the heart of autonomous skill generation.
type CoderService struct {
	mu     sync.RWMutex
	coders map[string]interfaces.SkillCoder
}

func NewCoderService(repo *db.SkillRepository, tm *ToolchainManager, skillsDir string, runner interfaces.SkillRunner) *CoderService {
	s := &CoderService{
		coders: make(map[string]interfaces.SkillCoder),
	}

	// Register Standard Coders
	s.RegisterCoder(&coders.GoCoder{Repo: repo, TM: tm, SkillsDir: skillsDir, Runner: runner})
	s.RegisterCoder(&coders.RustCoder{Repo: repo, TM: tm, SkillsDir: skillsDir, Runner: runner})
	s.RegisterCoder(&coders.ZigCoder{Repo: repo, TM: tm, SkillsDir: skillsDir, Runner: runner})

	return s
}

func (s *CoderService) RegisterCoder(c interfaces.SkillCoder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.coders[c.Language()] = c
}

// CreateSkill delegates compilation to the appropriate language coder
func (s *CoderService) CreateSkill(lang, name, description, paramsSchema, code string, permissions []string) error {
	s.mu.RLock()
	coder, ok := s.coders[lang]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("no coder registered for language: %s", lang)
	}
	return coder.Compile(name, description, paramsSchema, code, permissions)
}
