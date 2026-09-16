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
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/types"
)

// IntentResolver scores incoming intents against existing skills and specialists,
// acting as a fast-path filter before falling back to the LLM.
type IntentResolver struct {
	registry  *skills.Registry
	llmRouter *llm.Router
	skillRepo *db.SkillRepository
	specRepo  *db.SpecialistRepository
}

// NewIntentResolver constructs an IntentResolver
func NewIntentResolver(registry *skills.Registry, llmRouter *llm.Router, repo *db.SkillRepository, specRepo *db.SpecialistRepository) *IntentResolver {
	r := &IntentResolver{
		registry:  registry,
		llmRouter: llmRouter,
		skillRepo: repo,
		specRepo:  specRepo,
	}

	// Asynchronously bootstrap embedded models into memory if available
	go r.PrecomputeEmbeddings()
	return r
}

// Resolve analyzes the query and returns the best matching skill or specialist
func (r *IntentResolver) Resolve(ctx context.Context, query string) types.IntentMatch {
	lowerQuery := strings.ToLower(query)

	// 0. SPECIALIST MATCHING: Tiered confidence based on structural signals.
	// NOTE: @mention handling is done BEFORE the resolver (in orchestrator.resolveMention).
	// Here we only handle expertise-domain matches and bare name mentions.
	if r.specRepo != nil {
		allSpecs, err := r.specRepo.GetAllSpecialists()
		if err == nil {
			var bestSpecMatch types.IntentMatch

			for _, s := range allSpecs {
				lowerName := strings.ToLower(s.Name)
				nameInQuery := strings.Contains(lowerQuery, lowerName)

				if !nameInQuery {
					// Domain/Expertise keyword match WITHOUT specialist name → SignalDomainMatch (0.9)
					expertiseKeywords := strings.Fields(strings.ToLower(s.Expertise))
					for _, kw := range expertiseKeywords {
						if len(kw) > 3 && strings.Contains(lowerQuery, kw) {
							signal := types.SignalDomainMatch
							if signal.Confidence() > bestSpecMatch.Confidence {
								bestSpecMatch = types.IntentMatch{
									Type:       types.IntentTypeSpecialist,
									ID:         s.ID,
									Confidence: signal.Confidence(),
									Signal:     signal,
									Params:     map[string]interface{}{"task": query},
								}
							}
							break // One keyword hit is enough for this specialist
						}
					}
				} else {
					// Bare name mention WITHOUT @ prefix → SignalNameMention (0.6)
					signal := types.SignalNameMention
					if signal.Confidence() > bestSpecMatch.Confidence {
						bestSpecMatch = types.IntentMatch{
							Type:       types.IntentTypeSpecialist,
							ID:         s.ID,
							Confidence: signal.Confidence(),
							Signal:     signal,
							Params:     map[string]interface{}{"task": query},
						}
					}
				}
			}

			// If we have a strong domain match (>= 0.8), return it for fast-path
			if bestSpecMatch.Confidence >= 0.8 {
				return bestSpecMatch
			}
		}
	}

	// Semantic matching by using embeddings
	embedModel, embedProvider, err := r.llmRouter.GetEmbeddingProvider()
	if err == nil && embedProvider != nil {
		vecMatrix, err := embedProvider.Embed(ctx, embedModel.Model, []string{lowerQuery})
		if err == nil && len(vecMatrix) > 0 {
			queryVec := vecMatrix[0]
			bestSemanticMatch := types.IntentMatch{}

			for _, skill := range r.registry.GetAll() {
				if len(skill.Vector) > 0 {
					sim := CosineSimilarity(queryVec, skill.Vector)
					if sim > bestSemanticMatch.Confidence {
						bestSemanticMatch = types.IntentMatch{
							Type:       types.IntentTypeSkill,
							ID:         skill.ID,
							Confidence: sim,
							Params:     map[string]interface{}{"query": query},
						}
					}
				}
			}

			// If we match strongly semantically (over 0.8), return immediately!
			if bestSemanticMatch.Confidence > 0.8 {
				return bestSemanticMatch
			}
		}
	}

	// --- 2. FAILSAFE / ZERO-CONFIG LANE (Regex & Keyword Heuristics) ---
	bestMatch := types.IntentMatch{}

	for _, skill := range r.registry.GetAll() {
		confidence := 0.0

		// 1. Regex Match (Absolute Signal)
		if skill.ParamRegex != "" {
			if re, err := regexp.Compile("(?i)" + skill.ParamRegex); err == nil {
				if re.MatchString(lowerQuery) {
					// We have a direct regex match! It's an absolute lock.
					return types.IntentMatch{
						Type:       types.IntentTypeSkill,
						ID:         skill.ID,
						Confidence: 1.0,
						Params:     ExtractRegexParams(re, query),
					}
				}
			}
		}

		// 2. Keyword/Tag Scoring
		matchCount := 0
		totalKeywords := len(skill.Keywords) + len(skill.Tags)

		for _, kw := range skill.Keywords {
			if strings.Contains(lowerQuery, strings.ToLower(kw)) {
				matchCount++
			}
		}
		for _, tag := range skill.Tags {
			if strings.Contains(lowerQuery, strings.ToLower(tag)) {
				matchCount++
			}
		}

		// Heavy boost for exact skill name match
		if skill.Name != "" && strings.Contains(lowerQuery, strings.ToLower(skill.Name)) {
			matchCount += 2
			totalKeywords += 2 // Normalize the total to avoid >1.0 just from name
		}

		if totalKeywords > 0 {
			score := float64(matchCount) / float64(totalKeywords)
			if score > confidence {
				confidence = score
			}
		}

		if confidence > 1.0 {
			confidence = 1.0
		}

		// If this is the best so far, record it
		if confidence > bestMatch.Confidence {
			bestMatch = types.IntentMatch{
				Type:       types.IntentTypeSkill,
				ID:         skill.ID,
				Confidence: confidence,
				Params:     map[string]interface{}{"query": query},
			}
		}
	}

	return bestMatch
}

// ExtractRegexParams maps regex named capture groups to a map[string]interface{}
func ExtractRegexParams(re *regexp.Regexp, query string) map[string]interface{} {
	match := re.FindStringSubmatch(query)
	params := make(map[string]interface{})

	if len(match) > 0 {
		for i, name := range re.SubexpNames() {
			if i != 0 && name != "" {
				params[name] = match[i]
			}
		}
	}

	// Always provide the raw query as a fallback parameter
	params["query"] = query

	return params
}

// PrecomputeEmbeddings is a non-blocking cold boot that calculates
// mathematical vectors for every known skill via the selected embedding model.
func (r *IntentResolver) PrecomputeEmbeddings() {
	_, embedProvider, err := r.llmRouter.GetEmbeddingProvider()
	if err != nil || embedProvider == nil {
		return // Gracefully skip if no embedded model is configured
	}

	for _, rawSkill := range r.registry.GetAll() {
		// Only calculate if missing
		if len(rawSkill.Vector) == 0 {
			textContext := rawSkill.Name + " - " + rawSkill.Description
			if len(rawSkill.Examples) > 0 {
				textContext += " Examples: " + strings.Join(rawSkill.Examples, "; ")
			}
			matrix, err := embedProvider.Embed(context.Background(), "", []string{textContext})
			if err == nil && len(matrix) > 0 {
				rawSkill.Vector = matrix[0]
				r.registry.Register(rawSkill)      // Update memory
				if r.skillRepo != nil {
					_ = r.skillRepo.UpsertSkill(rawSkill) // Persist to DB
				}
			}
		}
	}
}

// CosineSimilarity measures the angle between two float arrays (exported for testing)
func CosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}
	var dot, magA, magB float64
	for i := 0; i < len(a); i++ {
		dot += float64(a[i] * b[i])
		magA += float64(a[i] * a[i])
		magB += float64(b[i] * b[i])
	}
	if magA == 0 || magB == 0 {
		return 0
	}
	return dot / (math.Sqrt(magA) * math.Sqrt(magB))
}

// FindSpecialistSemantically attempts to find a specialist by ID, exact name, or semantic closeness. 
// Uses a high confidence threshold (0.85) to avoid dangerous fuzzy actions.
func (r *IntentResolver) FindSpecialistSemantically(ctx context.Context, query string) (*types.Specialist, error) {
	if r.specRepo == nil {
		return nil, fmt.Errorf("specialist repository not attached to resolver")
	}

	allSpecs, err := r.specRepo.GetAllSpecialists()
	if err != nil || len(allSpecs) == 0 {
		return nil, fmt.Errorf("no specialists found")
	}

	lowerQuery := strings.ToLower(strings.TrimSpace(query))

	// 1. Literal Matches (ID or Name)
	for _, s := range allSpecs {
		if s.ID == lowerQuery || strings.ToLower(s.Name) == lowerQuery {
			return &s, nil
		}
	}

	// 2. Semantic Fallback
	embedModel, embedProvider, err := r.llmRouter.GetEmbeddingProvider()
	if err == nil && embedProvider != nil {
		vecMatrix, err := embedProvider.Embed(ctx, embedModel.Model, []string{lowerQuery})
		if err == nil && len(vecMatrix) > 0 {
			queryVec := vecMatrix[0]
			var bestMatch *types.Specialist
			var highestScore float64 = 0.0

			for _, s := range allSpecs {
				// Embed "Name. Expertise" to give the model rich context mapping
				doc := fmt.Sprintf("%s. %s", s.Name, s.Expertise)
				docVecs, err := embedProvider.Embed(ctx, embedModel.Model, []string{doc})
				if err == nil && len(docVecs) > 0 {
					sim := CosineSimilarity(queryVec, docVecs[0])
					if sim > highestScore {
						highestScore = sim
						// Create safely scoped pointer
						safeS := s
						bestMatch = &safeS
					}
				}
			}

			// Strict safety threshold
			if bestMatch != nil && highestScore > 0.85 {
				return bestMatch, nil
			}
		}
	}

	return nil, fmt.Errorf("no specialist found matching '%s' with high confidence", query)
}
