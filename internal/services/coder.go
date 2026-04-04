package services

import (
	"fmt"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/skills/coders"
)

// CoderService is the heart of autonomous skill generation.
type CoderService struct {
	coders map[string]coders.SkillCoder
}

func NewCoderService(repo *db.SkillRepository, tm *ToolchainManager, skillsDir string) *CoderService {
	s := &CoderService{
		coders: make(map[string]coders.SkillCoder),
	}

	// Register Standard Coders
	s.RegisterCoder(&coders.GoCoder{Repo: repo, TM: tm, SkillsDir: skillsDir})
	s.RegisterCoder(&coders.RustCoder{Repo: repo, TM: tm, SkillsDir: skillsDir})

	return s
}

func (s *CoderService) RegisterCoder(c coders.SkillCoder) {
	s.coders[c.Language()] = c
}

// CreateSkill delegates compilation to the appropriate language coder
func (s *CoderService) CreateSkill(lang, name, description, code string) error {
	coder, ok := s.coders[lang]
	if !ok {
		return fmt.Errorf("no coder registered for language: %s", lang)
	}
	return coder.Compile(name, description, code)
}

