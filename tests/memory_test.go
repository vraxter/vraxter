// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package tests

import (
	"math"
	"testing"

	"github.com/vraxter/vraxter/internal/db"
)

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		v1       []float32
		v2       []float32
		expected float32
	}{
		{
			name:     "Identical vectors",
			v1:       []float32{1.0, 0.0},
			v2:       []float32{1.0, 0.0},
			expected: 1.0,
		},
		{
			name:     "Orthogonal vectors",
			v1:       []float32{1.0, 0.0},
			v2:       []float32{0.0, 1.0},
			expected: 0.0,
		},
		{
			name:     "Mismatched lengths",
			v1:       []float32{1.0, 2.0, 3.0},
			v2:       []float32{1.0, 2.0},
			expected: 0.0,
		},
		{
			name:     "Zero vectors",
			v1:       []float32{0.0, 0.0},
			v2:       []float32{0.0, 0.0},
			expected: 0.0,
		},
		{
			name:     "Opposite directions",
			v1:       []float32{1.0, 1.0},
			v2:       []float32{-1.0, -1.0},
			expected: -1.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := db.CosineSimilarity(tt.v1, tt.v2)
			if math.Abs(float64(result-tt.expected)) > 1e-4 {
				t.Errorf("expected %f, got %f", tt.expected, result)
			}
		})
	}
}

func TestConvertBytesToFloat32Array(t *testing.T) {
	floats := []float32{1.5, -2.0, 3.14159}

	b, err := db.ConvertFloat32ArrayToBytes(floats)
	if err != nil {
		t.Fatalf("failed encoding: %v", err)
	}

	decoded, err := db.ConvertBytesToFloat32Array(b)
	if err != nil {
		t.Fatalf("failed decoding: %v", err)
	}

	if len(floats) != len(decoded) {
		t.Fatalf("expected length %d, got %d", len(floats), len(decoded))
	}

	for i := range floats {
		if math.Abs(float64(floats[i]-decoded[i])) > 1e-5 {
			t.Errorf("mismatch at idx %d: expected %f, got %f", i, floats[i], decoded[i])
		}
	}
}

func TestConvertBytesToFloat32Array_InvalidLength(t *testing.T) {
	b := []byte{0x00, 0x01, 0x02} // Not a multiple of 4

	_, err := db.ConvertBytesToFloat32Array(b)
	if err == nil {
		t.Fatalf("expected error for invalid byte length, got nil")
	}
}
