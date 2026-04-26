package registry

import (
	"fmt"
	"sync"

	"github.com/patagonicrune/vraxter/pkg/interfaces"
)

// FactoryFunc is a constructor function for a Provider adapter.
type FactoryFunc func(apiKey, baseURL string) interfaces.LLMProvider

var (
	providers = make(map[string]FactoryFunc)
	mu        sync.RWMutex
)

// Register adds a new provider factory to the registry.
func Register(pType string, factory FactoryFunc) {
	mu.Lock()
	defer mu.Unlock()
	providers[pType] = factory
}

// Create instantiates a provider by its type using the registered factory.
func Create(pType, apiKey, baseURL string) (interfaces.LLMProvider, error) {
	mu.RLock()
	defer mu.RUnlock()

	factory, ok := providers[pType]
	if !ok {
		return nil, fmt.Errorf("unsupported provider type: %s", pType)
	}

	return factory(apiKey, baseURL), nil
}

// GetSupportedProviders returns a list of all registered provider types.
func GetSupportedProviders() []string {
	mu.RLock()
	defer mu.RUnlock()

	var types []string
	for t := range providers {
		types = append(types, t)
	}
	return types
}
