package core

import (
	"context"
	"math"
	"regexp"
	"strings"

	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/pkg/types"
)

// IntentResolver scores incoming intents against existing skills
// acting as a fast-path filter before falling back to the LLM.
type IntentResolver struct {
	registry  *skills.Registry
	llmRouter *llm.Router
}

// NewIntentResolver constructs an IntentResolver
func NewIntentResolver(registry *skills.Registry, llmRouter *llm.Router) *IntentResolver {
	r := &IntentResolver{
		registry:  registry,
		llmRouter: llmRouter,
	}

	// Asynchronously bootstrap embedded models into memory if available
	go r.PrecomputeEmbeddings()
	return r
}

// Resolve analyzes the query and returns the best matching skill
func (r *IntentResolver) Resolve(ctx context.Context, query string) types.IntentMatch {
	lowerQuery := strings.ToLower(query)

	// --- 1. SEMANTIC VECTOR LANE ---
	embedModel, embedProvider, err := r.llmRouter.GetEmbeddingProvider()
	if err == nil && embedProvider != nil {
		vecMatrix, err := embedProvider.Embed(ctx, embedModel.Model, []string{lowerQuery})
		if err == nil && len(vecMatrix) > 0 {
			queryVec := vecMatrix[0]
			bestSemanticMatch := types.IntentMatch{}
			
			for _, skill := range r.registry.GetAll() {
				if len(skill.Vector) > 0 {
					sim := cosineSimilarity(queryVec, skill.Vector)
					if sim > bestSemanticMatch.Confidence {
						bestSemanticMatch = types.IntentMatch{
							SkillID:    skill.ID,
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
						SkillID:    skill.ID,
						Confidence: 1.0,
						Params:     extractRegexParams(re, query),
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
				SkillID:    skill.ID,
				Confidence: confidence,
				Params:     map[string]interface{}{"query": query},
			}
		}
	}

	return bestMatch
}

// extractRegexParams maps regex named capture groups to a map[string]interface{}
func extractRegexParams(re *regexp.Regexp, query string) map[string]interface{} {
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
			matrix, err := embedProvider.Embed(context.Background(), "", []string{textContext})
			if err == nil && len(matrix) > 0 {
				rawSkill.Vector = matrix[0]
				r.registry.Register(rawSkill) // Re-save to memory with the updated vector
			}
		}
	}
}

// cosineSimilarity measures the angle between two float arrays
func cosineSimilarity(a, b []float32) float64 {
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
