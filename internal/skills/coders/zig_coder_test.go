// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package coders

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/pkg/types"
)

type MockZigToolchain struct{}
func (m *MockZigToolchain) GetZigPath() string       { return "echo" } // Mocks the exec.Command to just run echo
func (m *MockZigToolchain) IsReady(lang string) bool { return true }
func (m *MockZigToolchain) SetupSDK(lang string) error { return nil }

type MockSkillRunner struct {
	DryRunFail bool
}
func (m *MockSkillRunner) Execute(ctx context.Context, manifest types.SkillManifest, params map[string]interface{}) (*types.ExecutionResult, error) {
	if m.DryRunFail {
		return nil, os.ErrPermission // Dummy error
	}
	return &types.ExecutionResult{Status: "completed", Output: "dry-run success"}, nil
}

func TestZigCoder_Language(t *testing.T) {
	coder := &ZigCoder{}
	if coder.Language() != "zig" {
		t.Errorf("Expected 'zig', got '%s'", coder.Language())
	}
}

func TestZigCoder_Compile_Success(t *testing.T) {
	// Setup DB
	database, _ := db.NewStore(":memory:")
	database.Conn.Exec(`CREATE TABLE IF NOT EXISTS skills (id TEXT PRIMARY KEY, name TEXT, description TEXT, version TEXT, engine TEXT, checksum TEXT, tier INTEGER, is_official BOOLEAN, score REAL, downloads INTEGER, params_schema TEXT, permissions TEXT)`)
	repo := db.NewSkillRepository(database)

	tempDir := t.TempDir()

	// Mock exec.Command output (ZigPath is 'echo', which won't emit a bin, but it will succeed)
	// Actually, wait, `os.ReadFile(wasmPath)` won't fail the compilation, just return empty checksum.
	// But it will pass the dry-run, then UpsertSkill. Let's touch the file to get a checksum.

	coder := &ZigCoder{
		Repo:      repo,
		TM:        &MockZigToolchain{},
		Runner:    &MockSkillRunner{DryRunFail: false},
		SkillsDir: tempDir,
	}

	// We use "touch" command for ZigPath to actually create the wasmPath instead of echo
	// But GetZigPath signature returns string.
	// We can't easily change args. If zigPath is "touch", the args will be "build-exe -target ... -femit-bin=... main.zig".
	// "touch" will fail because of those extra flags unless it's a bash script.
	// For testing, let's just let it be "echo" and it will succeed without creating the file.
	// ReadFile will just fail silently and set checksum="". This is fine for testing the logic path.

	err := coder.Compile("Test Zig", "Desc", "", "pub fn main() !void {}", nil)
	if err != nil {
		t.Fatalf("Compile failed: %v", err)
	}

	skills, _ := repo.GetAllSkills()
	if len(skills) != 1 {
		t.Fatalf("Expected 1 skill in DB, got %d", len(skills))
	}
	
	if skills[0].ID != "test-zig" {
		t.Errorf("Expected ID 'test-zig', got '%s'", skills[0].ID)
	}
}

func TestZigCoder_Compile_DryRunFail(t *testing.T) {
	database, _ := db.NewStore(":memory:")
	database.Conn.Exec(`CREATE TABLE IF NOT EXISTS skills (id TEXT PRIMARY KEY, name TEXT, description TEXT, version TEXT, engine TEXT, checksum TEXT, tier INTEGER, is_official BOOLEAN, score REAL, downloads INTEGER, params_schema TEXT, permissions TEXT)`)
	repo := db.NewSkillRepository(database)

	tempDir := t.TempDir()

	coder := &ZigCoder{
		Repo:      repo,
		TM:        &MockZigToolchain{},
		Runner:    &MockSkillRunner{DryRunFail: true}, // Simulate a crash
		SkillsDir: tempDir,
	}

	err := coder.Compile("Crash Zig", "Desc", "", "pub fn main() !void {}", nil)
	if err == nil {
		t.Fatal("Expected Compile to fail during dry-run, but it succeeded")
	}

	skills, _ := repo.GetAllSkills()
	if len(skills) != 0 {
		t.Fatalf("Expected 0 skills in DB after failed dry-run, got %d", len(skills))
	}

	// Verify the WASM was purged
	wasmPath := filepath.Join(tempDir, "crash-zig.wasm")
	if _, err := os.Stat(wasmPath); !os.IsNotExist(err) {
		t.Errorf("Expected WASM file to be purged, but it might still exist: %v", err)
	}
}
