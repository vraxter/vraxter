// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package tests

import (
	"testing"

	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/pkg/types"
)

func TestRegistry_RegisterAndFind(t *testing.T) {
	r := skills.NewRegistry()

	manifest := types.SkillManifest{
		ID:          "test-skill-1",
		Name:        "Test Skill",
		Description: "A test skill",
		Version:     "1.0.0",
		Command:     "/skills/test.wasm",
		Language:    "go",
	}

	if err := r.Register(manifest); err != nil {
		t.Fatalf("Register failed: %v", err)
	}

	found, err := r.FindSkill("test-skill-1")
	if err != nil {
		t.Fatalf("FindSkill failed: %v", err)
	}
	if found.Name != "Test Skill" {
		t.Errorf("expected 'Test Skill', got %q", found.Name)
	}
}

func TestRegistry_FindSkill_NotFound(t *testing.T) {
	r := skills.NewRegistry()
	_, err := r.FindSkill("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent skill, got nil")
	}
}

func TestRegistry_Register_EmptyID_ReturnsError(t *testing.T) {
	r := skills.NewRegistry()
	err := r.Register(types.SkillManifest{Name: "No ID Skill"})
	if err == nil {
		t.Fatal("expected error for empty ID, got nil")
	}
}

func TestRegistry_GetAll_ReturnsAllSkills(t *testing.T) {
	r := skills.NewRegistry()

	skills1 := []types.SkillManifest{
		{ID: "s1", Name: "Skill 1", Version: "1.0.0", Command: "/s1.wasm", Language: "go"},
		{ID: "s2", Name: "Skill 2", Version: "1.0.0", Command: "/s2.wasm", Language: "rust"},
		{ID: "s3", Name: "Skill 3", Version: "1.0.0", Command: "/s3.wasm", Language: "go"},
	}

	for _, s := range skills1 {
		r.Register(s)
	}

	all := r.GetAll()
	if len(all) != 3 {
		t.Errorf("expected 3 skills, got %d", len(all))
	}
}

func TestRegistry_GetAll_EmptyRegistry(t *testing.T) {
	r := skills.NewRegistry()
	all := r.GetAll()
	if len(all) != 0 {
		t.Errorf("expected 0 skills in empty registry, got %d", len(all))
	}
}

func TestRegistry_Register_UpdatesExisting(t *testing.T) {
	r := skills.NewRegistry()

	r.Register(types.SkillManifest{ID: "s1", Name: "Original", Version: "1.0.0", Command: "/s.wasm", Language: "go"})
	r.Register(types.SkillManifest{ID: "s1", Name: "Updated", Version: "2.0.0", Command: "/s.wasm", Language: "go"})

	found, _ := r.FindSkill("s1")
	if found.Name != "Updated" {
		t.Errorf("expected skill to be updated to 'Updated', got %q", found.Name)
	}
	if found.Version != "2.0.0" {
		t.Errorf("expected version 2.0.0, got %q", found.Version)
	}
}

func TestRegistry_ConcurrentRegisterAndGet(t *testing.T) {
	r := skills.NewRegistry()
	done := make(chan bool, 10)

	// Concurrent writes
	for i := 0; i < 5; i++ {
		go func(n int) {
			r.Register(types.SkillManifest{
				ID: "concurrent-skill", Name: "Concurrent", Version: "1.0.0", Command: "/s.wasm", Language: "go",
			})
			done <- true
		}(i)
	}
	// Concurrent reads
	for i := 0; i < 5; i++ {
		go func() {
			_ = r.GetAll()
			done <- true
		}()
	}

	for i := 0; i < 10; i++ {
		<-done
	}
}
