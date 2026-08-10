package db

import (
	"testing"
	"github.com/patagonicrune/vraxter/pkg/types"
)

func TestSkillsRepo_UpsertAndFind(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()
	repo := NewSkillRepository(store)

	manifest := types.SkillManifest{
		ID:          "test-skill",
		Name:        "Test Skill",
		Description: "A test skill",
		Version:     "1.0",
		Engine:      "wasm",
		Checksum:    "abcdef",
		Tier:        types.Tier2CommunityVerified,
		IsOfficial:  true,
		Score:       4.5,
		Downloads:   100,
		Permissions: []string{"network", "fs_read:/tmp"},
	}

	err := repo.UpsertSkill(manifest)
	if err != nil {
		t.Fatalf("Failed to upsert skill: %v", err)
	}

	skill, err := repo.FindSkill("test-skill")
	if err != nil {
		t.Fatalf("Failed to find skill: %v", err)
	}

	if skill.Name != "Test Skill" {
		t.Errorf("Expected name 'Test Skill', got '%s'", skill.Name)
	}
	if len(skill.Permissions) != 2 || skill.Permissions[0] != "network" {
		t.Errorf("Expected 2 permissions starting with network, got %v", skill.Permissions)
	}
	if !skill.IsOfficial {
		t.Error("Expected IsOfficial to be true")
	}

	// Test Update
	manifest.Score = 4.8
	err = repo.UpsertSkill(manifest)
	if err != nil {
		t.Fatalf("Failed to update skill: %v", err)
	}

	skill, _ = repo.FindSkill("test-skill")
	if skill.Score != 4.8 {
		t.Errorf("Expected updated score 4.8, got %f", skill.Score)
	}
}

func TestSkillsRepo_GetAllAndDelete(t *testing.T) {
	store, _ := NewStore(":memory:")
	defer store.Close()
	repo := NewSkillRepository(store)

	repo.UpsertSkill(types.SkillManifest{ID: "skill-1", Name: "S1"})
	repo.UpsertSkill(types.SkillManifest{ID: "skill-2", Name: "S2"})

	skills, err := repo.GetAllSkills()
	if err != nil {
		t.Fatalf("Failed to get all skills: %v", err)
	}
	if len(skills) != 2 {
		t.Errorf("Expected 2 skills, got %d", len(skills))
	}

	err = repo.DeleteSkill("skill-1")
	if err != nil {
		t.Fatalf("Failed to delete skill: %v", err)
	}

	skills, _ = repo.GetAllSkills()
	if len(skills) != 1 || skills[0].ID != "skill-2" {
		t.Errorf("Expected 1 skill 'skill-2', got %v", skills)
	}
}
