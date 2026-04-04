package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/patagonicrune/vraxter/pkg/types"
)

type Router struct {
	mu        sync.RWMutex
	providers map[string]Provider
	models    []types.ModelConfig
}

func NewRouter(models []types.ModelConfig) *Router {
	return &Router{
		providers: make(map[string]Provider),
		models:    models,
	}
}

func (r *Router) Register(modelID string, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[modelID] = p
}

func (r *Router) GetProvider(ctx context.Context, intent types.Intent) (types.ModelConfig, Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, config := range r.models {
		if p, ok := r.providers[config.ID]; ok {
			fmt.Printf("Using model: %s\n", config.Model)
			return config, p, nil
		}
	}
	return types.ModelConfig{}, nil, errors.New("no configured model providers available")
}

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
