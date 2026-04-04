package llm

import (
	"context"
	"errors"
	"fmt"

	"github.com/patagonicrune/vraxter/pkg/types"
)

// Router selects the best Provider for a given task/intent
type Router struct {
	providers map[string]Provider
	models    []types.ModelConfig
}

// NewRouter creates an instance initialized with the priority array
func NewRouter(models []types.ModelConfig) *Router {
	return &Router{
		providers: make(map[string]Provider),
		models:    models,
	}
}

// Register maps a Model ID to an implementation
func (r *Router) Register(modelID string, p Provider) {
	r.providers[modelID] = p
}

// GetProvider returns the highest-priority available provider (for single-shot use)
func (r *Router) GetProvider(ctx context.Context, intent types.Intent) (types.ModelConfig, Provider, error) {
	for _, config := range r.models {
		if p, ok := r.providers[config.ID]; ok {
			fmt.Printf("Using model: %s\n", config.Model)
			return config, p, nil
		}
	}
	return types.ModelConfig{}, nil, errors.New("no configured model providers available")
}

// GetOrderedProviders returns all registered providers in priority order for fallback iteration
func (r *Router) GetOrderedProviders() []struct {
	Config   types.ModelConfig
	Provider Provider
} {
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
