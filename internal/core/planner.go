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
	"github.com/vraxter/vraxter/pkg/types"
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
func (p *Planner) BuildPlan(ctx context.Context, query string, existingContext string, preferredModelID string, targetSpecialist ...string) (*Plan, error) {
	var leadSpec string
	if len(targetSpecialist) > 0 && targetSpecialist[0] != "" {
		leadSpec = strings.TrimPrefix(strings.TrimSpace(targetSpecialist[0]), "@")
	}

	// 1. Get ordered list of providers for the planning use-case
	providers := p.router.GetOrderedProvidersForUseCase("planning")
	if len(providers) == 0 {
		return nil, fmt.Errorf("no models available for planning")
	}

	// Prioritize preferred model (e.g. from session override or active model) if specified
	if preferredModelID != "" {
		target := strings.ToLower(strings.TrimSpace(preferredModelID))
		for idx, prov := range providers {
			if strings.ToLower(prov.Config.Model) == target ||
				strings.ToLower(prov.Config.Alias) == target ||
				strings.ToLower(prov.Config.ID) == target {
				if idx > 0 {
					reordered := make([]struct {
						Config   types.ModelConfig
						Provider llm.Provider
					}, 0, len(providers))
					reordered = append(reordered, prov)
					for i, other := range providers {
						if i != idx {
							reordered = append(reordered, other)
						}
					}
					providers = reordered
				}
				break
			}
		}
	}

	systemPrompt, err := prompts.RenderPlanner(prompts.PlannerParams{
		Query:               query,
		GitContext:          utils.CaptureGitContext(""),
		ExistingSpecialists: existingContext,
		TargetSpecialist:    leadSpec,
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

		userPrompt := fmt.Sprintf("Create a plan for: %s", query)
		if leadSpec != "" {
			userPrompt = fmt.Sprintf("Create an actionable plan led by specialist @%s for: %s. Assign implementation phases to '%s'.", leadSpec, query, leadSpec)
		}

		req := llm.CompletionRequest{
			Model: prov.Config.Model,
			Messages: []llm.Message{
				{Role: "system", Content: systemPrompt},
				{Role: "user", Content: userPrompt},
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

		// Clean out CoT / reasoning tags emitted by local thinking models (DeepSeek-R1, QwQ, etc.)
		content = stripThinkingTags(content)

		var narrative string
		var rawManifest string

		parts := strings.Split(content, "[MANIFEST]")
		if len(parts) > 1 {
			narrative = strings.TrimSpace(parts[0])
			rawManifest = strings.TrimSpace(parts[1])
		} else {
			rawManifest = content
		}

		// Always isolate between the first '{' and last '}' to strip markdown backticks and conversational text
		start := strings.Index(rawManifest, "{")
		end := strings.LastIndex(rawManifest, "}")
		if start != -1 && end != -1 && end > start {
			if narrative == "" && start > 0 {
				narrative = strings.TrimSpace(rawManifest[:start])
			}
			rawManifest = rawManifest[start : end+1]
		}

		plan, err := parsePlanJSON(rawManifest)
		if err != nil {
			// One-shot schema normalization retry for local models
			repairSchema := "{\"goal\":\"...\",\"reasoning\":\"...\",\"phases\":[{\"title\":\"...\",\"description\":\"...\",\"tasks\":[\"...\"],\"specialist_id\":\"supervisor\"}],\"estimations\":\"...\"}"
			if leadSpec != "" {
				repairSchema = fmt.Sprintf("{\"goal\":\"...\",\"reasoning\":\"...\",\"phases\":[{\"title\":\"...\",\"description\":\"...\",\"tasks\":[\"...\"],\"specialist_id\":\"%s\"}],\"estimations\":\"...\"}", leadSpec)
			}
			repairReq := llm.CompletionRequest{
				Model: prov.Config.Model,
				Messages: []llm.Message{
					{Role: "system", Content: "You are a JSON repair tool. Output ONLY valid JSON matching this schema: " + repairSchema + ". No markdown fences, no conversational text."},
					{Role: "user", Content: rawManifest},
				},
				Format: "json",
			}
			if repairResp, repairErr := prov.Provider.Generate(ctx, repairReq); repairErr == nil {
				repairContent := stripThinkingTags(repairResp.Content)
				rStart := strings.Index(repairContent, "{")
				rEnd := strings.LastIndex(repairContent, "}")
				if rStart != -1 && rEnd != -1 && rEnd > rStart {
					if repairedPlan, rErr := parsePlanJSON(repairContent[rStart : rEnd+1]); rErr == nil {
						plan = repairedPlan
						err = nil
					}
				}
			}
		}

		if err != nil {
			lastErr = fmt.Errorf("provider %s returned invalid manifest JSON: %w", prov.Config.Model, err)
			continue
		}

		// Success!
		if plan.Narrative == "" {
			plan.Narrative = narrative
		}
		if leadSpec != "" {
			for i := range plan.Phases {
				if plan.Phases[i].Specialist == "" || plan.Phases[i].Specialist == "supervisor" {
					plan.Phases[i].Specialist = leadSpec
				}
			}
		}
		plan.Status = "proposed"
		plan.CreatedAt = time.Now()
		return plan, nil
	}

	return nil, fmt.Errorf("all providers failed to generate a plan. Last error: %v", lastErr)
}

func stripThinkingTags(s string) string {
	for {
		start := strings.Index(s, "<think>")
		end := strings.Index(s, "</think>")
		if start != -1 && end != -1 && end > start {
			s = s[:start] + s[end+len("</think>"):]
			continue
		}
		break
	}
	for {
		start := strings.Index(s, "<thought>")
		end := strings.Index(s, "</thought>")
		if start != -1 && end != -1 && end > start {
			s = s[:start] + s[end+len("</thought>"):]
			continue
		}
		break
	}
	return s
}

func parsePlanJSON(raw string) (*Plan, error) {
	// Clean trailing commas before closing braces/brackets (common local-model syntax error)
	raw = strings.ReplaceAll(raw, ",}", "}")
	raw = strings.ReplaceAll(raw, ", }", "}")
	raw = strings.ReplaceAll(raw, ",\n}", "\n}")
	raw = strings.ReplaceAll(raw, ",]", "]")
	raw = strings.ReplaceAll(raw, ", ]", "]")
	raw = strings.ReplaceAll(raw, ",\n]", "\n]")

	var plan Plan
	if err := json.Unmarshal([]byte(raw), &plan); err == nil && len(plan.Phases) > 0 {
		return &plan, nil
	}

	// Dynamic fallback for non-standard field names
	var generic map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &generic); err != nil {
		return nil, err
	}

	if g, ok := generic["goal"].(string); ok {
		plan.Goal = g
	}
	if r, ok := generic["reasoning"].(string); ok {
		plan.Reasoning = r
	}
	for _, estKey := range []string{"estimations", "estimation", "estimate", "time"} {
		if e, ok := generic[estKey].(string); ok && e != "" {
			plan.Estimations = e
			break
		}
	}

	var rawPhases []interface{}
	for _, pKey := range []string{"phases", "steps", "tasks", "stages"} {
		if list, ok := generic[pKey].([]interface{}); ok && len(list) > 0 {
			rawPhases = list
			break
		}
	}

	for _, item := range rawPhases {
		pMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		var ph Phase
		if t, ok := pMap["title"].(string); ok {
			ph.Title = t
		} else if n, ok := pMap["name"].(string); ok {
			ph.Title = n
		}
		if d, ok := pMap["description"].(string); ok {
			ph.Description = d
		}
		for _, sKey := range []string{"specialist_id", "specialist", "agent", "role"} {
			if s, ok := pMap[sKey].(string); ok && s != "" {
				ph.Specialist = s
				break
			}
		}
		if ph.Specialist == "" {
			ph.Specialist = "supervisor"
		}

		for _, tKey := range []string{"tasks", "steps", "subtasks", "actions"} {
			if tList, ok := pMap[tKey].([]interface{}); ok {
				for _, sub := range tList {
					if subStr, ok := sub.(string); ok && strings.TrimSpace(subStr) != "" {
						ph.Tasks = append(ph.Tasks, subStr)
					}
				}
				break
			}
		}
		plan.Phases = append(plan.Phases, ph)
	}

	if len(plan.Phases) == 0 {
		return nil, fmt.Errorf("no phases found in generated plan JSON")
	}
	return &plan, nil
}
