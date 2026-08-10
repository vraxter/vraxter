package services

import (
	"fmt"
	"sync"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/skills/coders"
	"github.com/patagonicrune/vraxter/pkg/interfaces"
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
