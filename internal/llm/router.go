package llm

import (
	"context"
	"errors"
	"strings"
	"sync"

	"github.com/patagonicrune/vraxter/pkg/types"
)

type Router struct {
	mu        sync.RWMutex
	providers map[string]Provider
	models    []types.ModelConfig

	// routingCfg is an optional use-case routing table (see config.RoutingConfig).
	// The router holds it as an interface to avoid circular imports.
	routingOverrides map[string]string // useCase → modelID, pre-resolved at startup
}

// RouterEntry pairs a provider with its model config — used for testing.
type RouterEntry struct {
	Provider Provider
	Config   types.ModelConfig
}

func NewRouter(models []types.ModelConfig) *Router {
	return &Router{
		providers:        make(map[string]Provider),
		models:           models,
		routingOverrides: make(map[string]string),
	}
}

// NewRouterFromEntries builds a fully-wired Router directly from RouterEntry slices.
// Primarily used in tests to inject mock providers without touching the DB.
func NewRouterFromEntries(entries []RouterEntry) *Router {
	r := &Router{
		providers:        make(map[string]Provider),
		routingOverrides: make(map[string]string),
	}
	for _, e := range entries {
		r.models = append(r.models, e.Config)
		r.providers[e.Config.ID] = e.Provider
	}
	return r
}

func (r *Router) Register(modelID string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[modelID] = p
}

// SetUseCaseOverride tells the Router that queries classified as useCase should
// prefer the model with the given ID. Called once at startup from config.
func (r *Router) SetUseCaseOverride(useCase, modelID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routingOverrides[strings.ToLower(useCase)] = modelID
}

// GetProviderByID retrieves a specific provider by its ID, user-friendly Alias, or technical Model name.
func (r *Router) GetProviderByID(id string) (types.ModelConfig, Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	// Normalize Search: lowercase comparisons
	searchID := strings.ToLower(id)

	for _, config := range r.models {
		if config.ID == id || strings.ToLower(config.Alias) == searchID || strings.ToLower(config.Model) == searchID {
			if p, ok := r.providers[config.ID]; ok {
				return config, p, nil
			}
		}
	}
	return types.ModelConfig{}, nil, errors.New("model not found: " + id)
}

// GetProviderForUseCase returns the best model+provider for the given use-case tag.
// Priority: routing.yaml override → model.UseCases field match → priority order fallback.
// Zero LLM calls — pure local computation.
func (r *Router) GetProviderForUseCase(useCase string) (types.ModelConfig, Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if useCase == "" {
		return r.firstAvailable()
	}

	ucLower := strings.ToLower(useCase)

	// 1️⃣ Check routing.yaml explicit overrides (highest priority)
	if overrideID, ok := r.routingOverrides[ucLower]; ok && overrideID != "" {
		for _, config := range r.models {
			if config.ID == overrideID {
				if p, ok := r.providers[config.ID]; ok {
					return config, p, nil
				}
			}
		}
		// Override configured but provider wasn't registered — fall through
	}

	// 2️⃣ Check model.UseCases fields (set via `vraxter models add --use-cases`)
	for _, config := range r.models {
		if config.UseCases == "" {
			continue
		}
		for _, uc := range strings.Split(strings.ToLower(config.UseCases), ",") {
			if strings.TrimSpace(uc) == ucLower {
				if p, ok := r.providers[config.ID]; ok {
					return config, p, nil
				}
			}
		}
	}

	// 3️⃣ Priority-order fallback
	return r.firstAvailable()
}

// GetProvider routes the intent to the best available provider.
// It extracts the use-case from Intent.Query if present and uses GetProviderForUseCase.
func (r *Router) GetProvider(ctx context.Context, intent types.Intent) (types.ModelConfig, Provider, error) {
	return r.GetProviderForUseCase(ClassifyUseCase(intent.Query))
}

// GetOrderedProviders returns all registered providers in priority order.
// Accepts an optional useCase string to re-rank the preferred model first.
func (r *Router) GetOrderedProviders() []struct {
	Config   types.ModelConfig
	Provider Provider
} {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var ordered []struct {
		Config   types.ModelConfig
		Provider Provider
	}
	for _, config := range r.models {
		if p, ok := r.providers[config.ID]; ok {
			ordered = append(ordered, struct {
				Config   types.ModelConfig
				Provider Provider
			}{config, p})
		}
	}
	return ordered
}

// GetOrderedProvidersForUseCase returns providers in priority order, with the
// use-case preferred model moved to the front for cascading failover.
func (r *Router) GetOrderedProvidersForUseCase(useCase string) []struct {
	Config   types.ModelConfig
	Provider Provider
} {
	all := r.GetOrderedProviders()
	if useCase == "" || len(all) <= 1 {
		return all
	}

	preferredCfg, _, err := r.GetProviderForUseCase(useCase)
	if err != nil || preferredCfg.ID == "" {
		return all
	}

	// Move preferred model to front without re-allocating
	result := make([]struct {
		Config   types.ModelConfig
		Provider Provider
	}, 0, len(all))
	var rest []struct {
		Config   types.ModelConfig
		Provider Provider
	}
	for _, entry := range all {
		if entry.Config.ID == preferredCfg.ID {
			result = append(result, entry)
		} else {
			rest = append(rest, entry)
		}
	}
	return append(result, rest...)
}

func (r *Router) GetEmbeddingProvider() (types.ModelConfig, Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, config := range r.models {
		if strings.Contains(strings.ToLower(config.Capabilities), "embedding") {
			if p, ok := r.providers[config.ID]; ok {
				return config, p, nil
			}
		}
	}
	return types.ModelConfig{}, nil, errors.New("no embedding provider configured")
}

// firstAvailable returns the first registered model in priority order.
func (r *Router) firstAvailable() (types.ModelConfig, Provider, error) {
	for _, config := range r.models {
		if p, ok := r.providers[config.ID]; ok {
			return config, p, nil
		}
	}
	return types.ModelConfig{}, nil, errors.New("no configured model providers available")
}

// ─── Use-Case Classifier ─────────────────────────────────────────────────────
// ClassifyUseCase maps a natural language query to one of the standard use-case tags.
// This is a pure keyword heuristic — zero LLM calls, sub-millisecond latency.
// Tags: coding, writing, analysis, planning, math, general
func ClassifyUseCase(query string) string {
	q := strings.ToLower(query)

	codingKW := []string{
		"code", "function", "implement", "bug", "debug", "refactor", "test",
		"unit test", "compile", "syntax", "error", "class", "method", "api",
		"library", "package", "module", "script", "golang", "python", "rust",
		"javascript", "typescript", "sql", "query", "regex", "algorithm",
		"performance", "optimize", "bottleneck", "goroutine", "async", "wasm",
	}
	planningKW := []string{
		"plan", "roadmap", "architecture", "design", "blueprint", "strategy",
		"how should i", "what approach", "steps to", "implementation plan",
		"phases", "sprint", "milestone", "structure", "scaffold",
	}
	writingKW := []string{
		"write", "draft", "blog", "article", "essay", "email", "letter",
		"documentation", "readme", "report", "summarize this text",
		"rewrite", "paraphrase", "creative", "story", "marketing",
	}
	analysisKW := []string{
		"analyze", "compare", "evaluate", "research", "explain", "breakdown",
		"review", "assess", "summarize", "pros and cons", "tradeoffs",
		"difference between", "what is", "why does", "how does",
	}
	mathKW := []string{
		"calculate", "compute", "formula", "equation", "integral", "derivative",
		"probability", "statistics", "proof", "theorem", "matrix", "algebra",
		"logic", "boolean",
	}

	score := func(kws []string) int {
		count := 0
		for _, kw := range kws {
			if strings.Contains(q, kw) {
				count++
			}
		}
		return count
	}

	scores := map[string]int{
		"coding":   score(codingKW),
		"planning": score(planningKW),
		"writing":  score(writingKW),
		"analysis": score(analysisKW),
		"math":     score(mathKW),
	}

	best, bestScore := "general", 0
	for tag, s := range scores {
		if s > bestScore {
			bestScore = s
			best = tag
		}
	}
	return best
}
