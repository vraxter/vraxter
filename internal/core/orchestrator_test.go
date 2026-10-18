// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package core

import (
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/pkg/types"
)

func newTestOrchestratorForPlanning(t *testing.T) *Orchestrator {
	t.Helper()
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory store: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	specRepo := db.NewSpecialistRepository(store)
	_ = specRepo.CreateSpecialist(types.Specialist{
		ID:           "sec-uuid-001",
		Name:         "security-auditor",
		Expertise:    "Cybersecurity audits and hardening",
		ModelID:      "model-security",
		SystemPrompt: "Act as a security auditor.",
		CreatedAt:    time.Now(),
	})
	_ = specRepo.CreateSpecialist(types.Specialist{
		ID:           "staff-uuid-002",
		Name:         "staff-engineer",
		Expertise:    "Distributed architecture",
		ModelID:      "model-staff",
		SystemPrompt: "Act as a staff engineer.",
		CreatedAt:    time.Now(),
	})

	return &Orchestrator{
		SpecialistRepo: specRepo,
	}
}

func TestDetectPlanIntent(t *testing.T) {
	o := newTestOrchestratorForPlanning(t)

	tests := []struct {
		name          string
		input         string
		currentSpecID string
		wantPlan      bool
		wantQuery     string
		wantLeadSpec  string
	}{
		{
			name:          "Plan with specialist mention at start",
			input:         "/plan @security-auditor audit authentication endpoints",
			currentSpecID: "",
			wantPlan:      true,
			wantQuery:     "audit authentication endpoints",
			wantLeadSpec:  "security-auditor",
		},
		{
			name:          "Mention first then /plan",
			input:         "@security-auditor /plan audit authentication endpoints",
			currentSpecID: "",
			wantPlan:      true,
			wantQuery:     "audit authentication endpoints",
			wantLeadSpec:  "security-auditor",
		},
		{
			name:          "Plan with specialist mention at end",
			input:         "/plan audit authentication endpoints @security-auditor",
			currentSpecID: "",
			wantPlan:      true,
			wantQuery:     "audit authentication endpoints",
			wantLeadSpec:  "security-auditor",
		},
		{
			name:          "Plan with contextual specialist ID and no explicit mention",
			input:         "/plan optimize distributed caching layer",
			currentSpecID: "staff-uuid-002",
			wantPlan:      true,
			wantQuery:     "optimize distributed caching layer",
			wantLeadSpec:  "staff-engineer",
		},
		{
			name:          "Plan with no specialist",
			input:         "/plan refactor user authentication",
			currentSpecID: "",
			wantPlan:      true,
			wantQuery:     "refactor user authentication",
			wantLeadSpec:  "",
		},
		{
			name:          "Amend plan with specialist mention",
			input:         "AMEND PLAN: @security-auditor add rate limiting tests",
			currentSpecID: "",
			wantPlan:      true,
			wantQuery:     "add rate limiting tests",
			wantLeadSpec:  "security-auditor",
		},
		{
			name:          "Amend plan without specialist",
			input:         "AMEND PLAN: include unit tests for edge cases",
			currentSpecID: "",
			wantPlan:      true,
			wantQuery:     "include unit tests for edge cases",
			wantLeadSpec:  "",
		},
		{
			name:          "Normal chat mention to specialist without /plan",
			input:         "@security-auditor how do I prevent SQL injections?",
			currentSpecID: "",
			wantPlan:      false,
			wantQuery:     "@security-auditor how do I prevent SQL injections?",
			wantLeadSpec:  "",
		},
		{
			name:          "Ordinary sentence mentioning plan as a noun",
			input:         "Can you explain the plan of attack for database migration?",
			currentSpecID: "",
			wantPlan:      false,
			wantQuery:     "Can you explain the plan of attack for database migration?",
			wantLeadSpec:  "",
		},
		{
			name:          "Empty plan query with mention",
			input:         "/plan @security-auditor",
			currentSpecID: "",
			wantPlan:      true,
			wantQuery:     "",
			wantLeadSpec:  "security-auditor",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			isPlan, query, leadSpec := o.detectPlanIntent(tc.input, tc.currentSpecID)

			if isPlan != tc.wantPlan {
				t.Errorf("detectPlanIntent(%q) isPlan = %v, want %v", tc.input, isPlan, tc.wantPlan)
			}
			if isPlan {
				if query != tc.wantQuery {
					t.Errorf("detectPlanIntent(%q) query = %q, want %q", tc.input, query, tc.wantQuery)
				}
				if tc.wantLeadSpec == "" && leadSpec != nil {
					t.Errorf("detectPlanIntent(%q) expected nil leadSpec, got %v", tc.input, leadSpec.Name)
				} else if tc.wantLeadSpec != "" {
					if leadSpec == nil {
						t.Errorf("detectPlanIntent(%q) expected leadSpec %q, got nil", tc.input, tc.wantLeadSpec)
					} else if leadSpec.Name != tc.wantLeadSpec {
						t.Errorf("detectPlanIntent(%q) leadSpec = %q, want %q", tc.input, leadSpec.Name, tc.wantLeadSpec)
					}
				}
			}
		})
	}
}

func TestResolveSpecialist(t *testing.T) {
	o := newTestOrchestratorForPlanning(t)

	// Test by ID
	spec, err := o.resolveSpecialist("sec-uuid-001")
	if err != nil || spec.Name != "security-auditor" {
		t.Fatalf("resolveSpecialist(ID) failed: %v", err)
	}

	// Test by Name without @
	spec, err = o.resolveSpecialist("security-auditor")
	if err != nil || spec.ID != "sec-uuid-001" {
		t.Fatalf("resolveSpecialist(name) failed: %v", err)
	}

	// Test by Name with @
	spec, err = o.resolveSpecialist("@staff-engineer")
	if err != nil || spec.ID != "staff-uuid-002" {
		t.Fatalf("resolveSpecialist(@name) failed: %v", err)
	}

	// Test case insensitive
	spec, err = o.resolveSpecialist("@Security-Auditor")
	if err != nil || spec.ID != "sec-uuid-001" {
		t.Fatalf("resolveSpecialist(case-insensitive) failed: %v", err)
	}

	// Test supervisor or vraxter
	_, err = o.resolveSpecialist("supervisor")
	if err == nil {
		t.Fatalf("resolveSpecialist('supervisor') should return error")
	}
	_, err = o.resolveSpecialist("vraxter")
	if err == nil {
		t.Fatalf("resolveSpecialist('vraxter') should return error")
	}
}

