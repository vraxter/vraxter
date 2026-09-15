// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package tests

import (
	"context"
	"regexp"
	"testing"

	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/pkg/types"
)

func TestExtractRegexParams(t *testing.T) {
	re := regexp.MustCompile(`(?i)create ticket (?P<title>.+) for (?P<user>.+)`)
	query := "Create ticket Server Down for admin"

	params := core.ExtractRegexParams(re, query)

	if params["title"] != "Server Down" {
		t.Errorf("expected title 'Server Down', got %v", params["title"])
	}
	if params["user"] != "admin" {
		t.Errorf("expected user 'admin', got %v", params["user"])
	}
	if params["query"] != query {
		t.Errorf("expected fallback query '%s', got %v", query, params["query"])
	}
}

func TestResolve_RegexFailsafe(t *testing.T) {
	// Create an empty memory SQLite DB for testing
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create memory db: %v", err)
	}
	defer store.Close()

	reg := skills.NewRegistry()

	// Inject a stub skill
	reg.Register(types.SkillManifest{
		ID:         "ticket_tool",
		Name:       "Ticket Tool",
		Tags:       []string{"ticket", "jira"},
		ParamRegex: `create ticket (?P<title>.+)`,
	})
	reg.Register(types.SkillManifest{
		ID:         "unrelated_tool",
		Name:       "Calculator",
		Keywords:   []string{"math", "calc"},
	})

	router := llm.NewRouter(nil)
	skillRepo := db.NewSkillRepository(store)
	resolver := core.NewIntentResolver(reg, router, skillRepo, nil)

	ctx := context.Background()

	// 1. Direct Regex Match (Confidence 1.0)
	match := resolver.Resolve(ctx, "create ticket Something Borked")
	if match.ID != "ticket_tool" {
		t.Errorf("expected ticket_tool, got %s", match.ID)
	}
	if match.Confidence != 1.0 {
		t.Errorf("expected confidence 1.0, got %f", match.Confidence)
	}
	if match.Params["title"] != "Something Borked" {
		t.Errorf("expected parameter title='Something Borked', got %v", match.Params["title"])
	}

	// 2. Keyword heuristic fallback
	matchKw := resolver.Resolve(ctx, "math stuff")
	if matchKw.ID != "unrelated_tool" {
		t.Errorf("expected unrelated_tool, got %s", matchKw.ID)
	}
	if matchKw.Confidence <= 0.0 {
		t.Errorf("expected confidence > 0.0 for keyword match, got %f", matchKw.Confidence)
	}
}
