// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package services

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/db"
)

func writeMockWasm(t *testing.T, dir, filename string) string {
	t.Helper()
	p := filepath.Join(dir, filename)
	// WASM magic bytes: \x00asm followed by version 1
	data := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	if err := os.WriteFile(p, data, 0755); err != nil {
		t.Fatalf("failed to write mock wasm: %v", err)
	}
	return p
}

func TestRegisterFromWASM_SuccessAndAutoDiscovery(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create memory store: %v", err)
	}
	defer store.Close()

	skillsStorage := filepath.Join(tmpDir, "internal_skills")
	svc := NewSkillService(db.NewSkillRepository(store), skillsStorage)

	srcDir := filepath.Join(tmpDir, "source_netscan")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatalf("failed to create srcDir: %v", err)
	}

	wasmPath := writeMockWasm(t, srcDir, "netscan.wasm")
	manifestJSON := `{
		"name": "Network Scanner",
		"description": "Scans local subnets for open TCP ports",
		"version": "2.1.0",
		"permissions": ["network:egress"],
		"params_schema": {
			"type": "object",
			"properties": {
				"cidr": {"type": "string", "description": "Target CIDR block"}
			},
			"required": ["cidr"]
		}
	}`
	manifestPath := filepath.Join(srcDir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(manifestJSON), 0644); err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	// 1. Inject by specifying wasm file without manifest flag -> auto-discovers adjacent manifest.json
	m, err := svc.RegisterFromWASM(wasmPath, "")
	if err != nil {
		t.Fatalf("RegisterFromWASM failed: %v", err)
	}

	if m.ID != "netscan" {
		t.Errorf("expected ID 'netscan', got '%s'", m.ID)
	}
	if m.Name != "Network Scanner" {
		t.Errorf("expected Name 'Network Scanner', got '%s'", m.Name)
	}
	if m.Version != "2.1.0" {
		t.Errorf("expected Version '2.1.0', got '%s'", m.Version)
	}
	if !strings.Contains(m.ParamsSchema, "cidr") {
		t.Errorf("expected ParamsSchema to contain 'cidr', got '%s'", m.ParamsSchema)
	}
	if m.Checksum == "" {
		t.Errorf("expected Checksum to be calculated, got empty")
	}

	// Verify skill binary was copied to internal storage
	destWasm := filepath.Join(skillsStorage, "netscan.wasm")
	if _, err := os.Stat(destWasm); err != nil {
		t.Errorf("expected installed wasm to exist in %s: %v", destWasm, err)
	}
}

func TestRegisterFromWASM_DirectoryInjection(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := db.NewStore(":memory:")
	defer store.Close()

	skillsStorage := filepath.Join(tmpDir, "internal_skills")
	svc := NewSkillService(db.NewSkillRepository(store), skillsStorage)

	pkgDir := filepath.Join(tmpDir, "quarantine_pkg")
	if err := os.MkdirAll(pkgDir, 0755); err != nil {
		t.Fatalf("failed to create pkgDir: %v", err)
	}

	writeMockWasm(t, pkgDir, "quarantine.wasm")
	manifestJSON := `{"name": "Host Quarantine", "desc": "Isolates compromised host"}`
	_ = os.WriteFile(filepath.Join(pkgDir, "manifest.json"), []byte(manifestJSON), 0644)

	// Inject by directory path
	m, err := svc.RegisterFromWASM(pkgDir, "")
	if err != nil {
		t.Fatalf("Directory injection failed: %v", err)
	}
	if m.ID != "quarantine" {
		t.Errorf("expected ID 'quarantine', got '%s'", m.ID)
	}
	if m.Description != "Isolates compromised host" {
		t.Errorf("expected Description 'Isolates compromised host', got '%s'", m.Description)
	}
}

func TestRegisterFromWASM_InvalidFiles(t *testing.T) {
	tmpDir := t.TempDir()
	store, _ := db.NewStore(":memory:")
	defer store.Close()

	svc := NewSkillService(db.NewSkillRepository(store), filepath.Join(tmpDir, "skills"))

	// 1. Non-existent path
	_, err := svc.RegisterFromWASM(filepath.Join(tmpDir, "missing.wasm"), "")
	if err == nil {
		t.Errorf("expected error on missing file")
	}

	// 2. Non-.wasm extension
	txtFile := filepath.Join(tmpDir, "script.sh")
	_ = os.WriteFile(txtFile, []byte("#!/bin/bash\necho hello"), 0755)
	_, err = svc.RegisterFromWASM(txtFile, "")
	if err == nil || !strings.Contains(err.Error(), ".wasm") {
		t.Errorf("expected .wasm extension error, got: %v", err)
	}

	// 3. Corrupt/fake WASM file without \x00asm magic bytes
	fakeWasm := filepath.Join(tmpDir, "corrupted.wasm")
	_ = os.WriteFile(fakeWasm, []byte("NOT_A_WASM_BINARY"), 0755)
	_, err = svc.RegisterFromWASM(fakeWasm, "")
	if err == nil || !strings.Contains(err.Error(), "magic header") {
		t.Errorf("expected magic header error, got: %v", err)
	}
}

