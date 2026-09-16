// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/prompts"
	"github.com/vraxter/vraxter/internal/utils"
)

// Plan represents an autonomous execution strategy broken down into observable phases.
type Plan struct {
	Narrative   string   `json:"narrative,omitempty"` // Detailed Markdown reasoning
	Goal        string   `json:"goal"`
	Reasoning   string   `json:"reasoning"`
	Phases      []Phase  `json:"phases"`
	Estimations string   `json:"estimations,omitempty"`
	Status      string   `json:"status"` // "proposed", "accepted", "rejected", "running", "completed"
	CreatedAt   time.Time `json:"created_at"`
}

// Phase is a logical group of tasks assigned to a specific specialist or the general engine.
type Phase struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Tasks       []string `json:"tasks"`
	Specialist  string   `json:"specialist_id"` // Matches ID of an existing specialist or "supervisor"
}

// Planner is responsible for analyzing complex tasks and generating structured plans.
type Planner struct {
	repo   *db.ChatRepository
	router *llm.Router
}

func NewPlanner(repo *db.ChatRepository, router *llm.Router) *Planner {
	return &Planner{
		repo:   repo,
		router: router,
	}
}

// BuildPlan asks an LLM to generate a structured implementation plan for a query.
func (p *Planner) BuildPlan(ctx context.Context, query string, existingContext string) (*Plan, error) {
	// 1. Get ordered list of providers for the planning use-case
	providers := p.router.GetOrderedProvidersForUseCase("planning")
	if len(providers) == 0 {
		return nil, fmt.Errorf("no models available for planning")
	}

	systemPrompt, err := prompts.RenderPlanner(prompts.PlannerParams{
		Query:               query,
		GitContext:          utils.CaptureGitContext(""),
		ExistingSpecialists: existingContext,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to render planner prompt: %w", err)
	}

	var lastErr error
	// 2. Iterate through providers until we get a successful plan
	for _, prov := range providers {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		req := llm.CompletionRequest{
			Model: prov.Config.Model,
			Messages: []llm.Message{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: fmt.Sprintf("Create a plan for: %s", query)},
			},
			Format: "json",
		}

		resp, err := prov.Provider.Generate(ctx, req)
		if err != nil {
			lastErr = fmt.Errorf("provider %s failed: %w", prov.Config.Model, err)
			continue
		}

		// Success logic for hybrid parsing
		content := resp.Content
		var narrative string
		var manifestJSON string

		parts := strings.Split(content, "[MANIFEST]")
		if len(parts) > 1 {
			narrative = strings.TrimSpace(parts[0])
			manifestJSON = strings.TrimSpace(parts[1])
		} else {
			// Fallback: search for first { and last }
			start := strings.Index(content, "{")
			end := strings.LastIndex(content, "}")
			if start != -1 && end != -1 && end > start {
				narrative = strings.TrimSpace(content[:start])
				manifestJSON = content[start : end+1]
			} else {
				lastErr = fmt.Errorf("provider %s failed to return a [MANIFEST] section", prov.Config.Model)
				continue
			}
		}

		var plan Plan
		if err := json.Unmarshal([]byte(manifestJSON), &plan); err != nil {
			lastErr = fmt.Errorf("provider %s returned invalid manifest JSON: %w", prov.Config.Model, err)
			continue
		}

		// Success!
		plan.Narrative = narrative
		plan.Status = "proposed"
		plan.CreatedAt = time.Now()
		return &plan, nil
	}

	return nil, fmt.Errorf("all providers failed to generate a plan. Last error: %v", lastErr)
}
