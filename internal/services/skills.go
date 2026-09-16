// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package services

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/types"
)

// SkillService handles installation and registration of system skills
type SkillService struct {
	repo      *db.SkillRepository
	skillsDir string
}

func NewSkillService(repo *db.SkillRepository, skillsDir string) *SkillService {
	return &SkillService{repo: repo, skillsDir: skillsDir}
}

// InstallSkill persists a new skill to the database after copying the binary to the internal storage
func (s *SkillService) InstallSkill(m types.SkillManifest) error {
	// 1. Ensure the destination directory exists
	if err := os.MkdirAll(s.skillsDir, 0700); err != nil {
		return fmt.Errorf("failed to ensure skills directory: %w", err)
	}

	// 2. Identify source and destination
	srcPath := m.Command
	filename := m.ID + filepath.Ext(srcPath)
	destPath := filepath.Join(s.skillsDir, filename)

	// 3. Copy if needed
	if srcPath != destPath {
		if err := copyFile(srcPath, destPath); err != nil {
			return fmt.Errorf("failed to copy skill binary: %w", err)
		}
	}
	m.Command = destPath

	// 4. Calculate Integrity Checksum (Phase 12)
	checksum, err := s.CalculateHash(m.Command)
	if err == nil {
		m.Checksum = checksum
	}

	return s.repo.UpsertSkill(m)
}


func (s *SkillService) CalculateHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:]), nil
}

func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	if err != nil {
		return err
	}

	// Make executable if it's a binary/wasm (usually just keep bits or set 0755)
	return os.Chmod(dst, 0755)
}


// LoadAllIntoRegistry fetches all skills from DB and fills the memory registry
func (s *SkillService) LoadAllIntoRegistry(reg *skills.Registry) error {
	all, err := s.repo.GetAllSkills()
	if err != nil {
		return err
	}
	for _, sm := range all {
		reg.Register(sm)
	}
	log.Printf("Skills: Loaded %d tools from DB into registry", len(all))
	return nil
}

// ListSkills returns all installed metadata
func (s *SkillService) ListSkills() ([]types.SkillManifest, error) {
	return s.repo.GetAllSkills()
}

func (s *SkillService) GetSkill(id string) (types.SkillManifest, error) {
	all, err := s.ListSkills()
	if err != nil {
		return types.SkillManifest{}, err
	}
	for _, sm := range all {
		if sm.ID == id {
			return sm, nil
		}
	}
	return types.SkillManifest{}, fmt.Errorf("skill '%s' not found", id)
}

func (s *SkillService) UpdateSkill(m types.SkillManifest) error {
	return s.repo.UpsertSkill(m)
}

func (s *SkillService) DeleteSkill(id string) error {
	// Ideally we would also delete the binary here
	return s.repo.DeleteSkill(id)
}

func (s *SkillService) RegisterFromWASM(path string, manifestPath string) error {
	id := filepath.Base(path)
	id = id[:len(id)-len(filepath.Ext(id))]

	m := types.SkillManifest{
		ID:          id,
		Name:        id,
		Description: "Injected local skill",
		Version:     "1.0.0",
		Engine:      "wasm",
		Command:     path,
	}

	if manifestPath != "" {
		data, err := os.ReadFile(manifestPath)
		if err != nil {
			return fmt.Errorf("failed to read custom manifest file: %w", err)
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return fmt.Errorf("failed to parse custom manifest json: %w", err)
		}
		
		// Security Overrides: Don't trust the custom manifest blindly
		m.Command = path       // Force the binary to be the one injected
		m.IsOfficial = false   // Cannot fake official status
		m.Engine = "wasm"      // Force WASM sandboxing
		// Note: Checksum is already safely recalculated inside InstallSkill()
	}

	return s.InstallSkill(m)
}
