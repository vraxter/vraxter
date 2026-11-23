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
	"strings"
	"testing"

	"github.com/vraxter/vraxter/pkg/types"
)

func TestRunner_RejectsOversizedWASMBinary(t *testing.T) {
	rn := NewRunner()
	defer rn.Close(context.Background())

	if rn.MaxBinaryBytes != MaxWASMBinaryBytes {
		t.Fatalf("expected default MaxBinaryBytes=%d, got %d", MaxWASMBinaryBytes, rn.MaxBinaryBytes)
	}

	// Lower cap for a fast unit test without writing 50+ MB to disk
	rn.MaxBinaryBytes = 1024

	oversizedPath := filepath.Join(t.TempDir(), "huge.wasm")
	if err := os.WriteFile(oversizedPath, make([]byte, 2048), 0600); err != nil {
		t.Fatalf("write oversized file: %v", err)
	}

	_, err := rn.Execute(context.Background(), types.SkillManifest{
		ID:      "huge-skill",
		Command: oversizedPath,
		Engine:  "wasm",
		Tier:    types.Tier3Unverified,
	}, nil)
	if err == nil {
		t.Fatal("expected Runner.Execute to reject .wasm exceeding MaxBinaryBytes")
	}
	if !strings.Contains(err.Error(), "exceeds maximum allowed size") {
		t.Fatalf("expected size-limit error, got: %v", err)
	}
}
