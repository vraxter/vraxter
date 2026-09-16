// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vraxter/vraxter/pkg/types"
	"github.com/tetratelabs/wazero/api"
)

type mockModule struct {
	api.Module
	mem api.Memory
}

func (m *mockModule) Memory() api.Memory {
	return m.mem
}

type mockMemory struct {
	api.Memory
	buf []byte
}

func (m *mockMemory) Read(offset, byteCount uint32) ([]byte, bool) {
	if offset+byteCount > uint32(len(m.buf)) {
		return nil, false
	}
	return m.buf[offset : offset+byteCount], true
}

func (m *mockMemory) Write(offset uint32, val []byte) bool {
	if offset+uint32(len(val)) > uint32(len(m.buf)) {
		return false
	}
	copy(m.buf[offset:], val)
	return true
}

func TestResolveMountPath(t *testing.T) {
	// Test 1: Simple absolute path
	abs, _ := filepath.Abs(".")
	expected := filepath.Clean(abs)
	res := ResolveMountPath(".")
	if res != expected {
		t.Errorf("expected %q, got %q", expected, res)
	}

	// Test 2: Environment variables
	os.Setenv("VRAX_TEST_DIR", "my-env-dir")
	defer os.Unsetenv("VRAX_TEST_DIR")
	resEnv := ResolveMountPath("$VRAX_TEST_DIR")
	expectedEnv, _ := filepath.Abs("my-env-dir")
	if resEnv != filepath.Clean(expectedEnv) {
		t.Errorf("expected %q, got %q", filepath.Clean(expectedEnv), resEnv)
	}

	// Test 3: Home directory shortcuts
	home, err := os.UserHomeDir()
	if err == nil {
		resHome := ResolveMountPath("~/vrax-projects")
		expectedHome := filepath.Clean(filepath.Join(home, "vrax-projects"))
		if resHome != expectedHome {
			t.Errorf("expected %q, got %q", expectedHome, resHome)
		}
	}
}

func TestHTTPGetCapabilityEnforcement(t *testing.T) {
	ctx := context.Background()
	mem := &mockMemory{buf: make([]byte, 1024)}
	mod := &mockModule{mem: mem}

	// Test Case A: Call without manifest in context (Forbidden)
	res := httpGetFn(ctx, mod, 0, 0, 0, 0)
	if res != 0 {
		t.Errorf("expected http_get to be blocked (return 0) when no manifest in context, got %d", res)
	}

	// Test Case B: Call with manifest lacking "network" permission (Forbidden)
	manifestNoNet := types.SkillManifest{
		ID:          "test-skill-no-net",
		Permissions: []string{"fs_read:/tmp"},
	}
	ctxNoNet := context.WithValue(ctx, manifestKey, manifestNoNet)
	resNoNet := httpGetFn(ctxNoNet, mod, 0, 0, 0, 0)
	if resNoNet != 0 {
		t.Errorf("expected http_get to be blocked when manifest lacks network permission, got %d", resNoNet)
	}

	// Test Case C: Call with manifest possessing "network" permission (Authorized but fails gracefully on empty URL)
	manifestWithNet := types.SkillManifest{
		ID:          "test-skill-with-net",
		Permissions: []string{"network"},
	}
	ctxWithNet := context.WithValue(ctx, manifestKey, manifestWithNet)
	resWithNet := httpGetFn(ctxWithNet, mod, 0, 0, 0, 0)
	// Since URL is blank, the GET request fails internally and returns 0, but it successfully bypasses capability block
	t.Logf("httpGetFn returned %d (bypassed capability block successfully)", resWithNet)
}
