// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// RoutingConfig maps use-case tags to model ID prefixes or aliases.
// It is stored at <AppDir>/routing.yaml and is the primary way users
// tell Vraxter which model to prefer for a given type of task.
//
// Example routing.yaml:
//
//	routes:
//	  coding: claude-3-5
//	  writing: gpt-4o
//	  analysis: gemini
//	  general: llama3
type RoutingConfig struct {
	// Routes maps a use-case tag → model ID prefix or alias fragment.
	Routes map[string]string
}

// DefaultRoutingConfig returns an empty, ready-to-use config.
func DefaultRoutingConfig() *RoutingConfig {
	return &RoutingConfig{Routes: make(map[string]string)}
}

// LoadRoutingConfig reads the routing.yaml from the given appDir.
// If the file does not exist it returns a default empty config (no error).
// Uses a simple hand-rolled parser to stay dependency-free.
func LoadRoutingConfig(appDir string) (*RoutingConfig, error) {
	path := filepath.Join(appDir, "routing.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultRoutingConfig(), nil
		}
		return nil, fmt.Errorf("reading routing.yaml: %w", err)
	}
	return parseRoutingYAML(string(data))
}

// parseRoutingYAML parses only the "routes:" block from the YAML.
// Pure Go, no third-party dependency. Handles comments, blank lines.
func parseRoutingYAML(content string) (*RoutingConfig, error) {
	cfg := DefaultRoutingConfig()
	inRoutes := false

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		// Skip blank lines and comments
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Detect "routes:" section header
		if strings.TrimRight(trimmed, " \t") == "routes:" {
			inRoutes = true
			continue
		}

		// Detect any new top-level key (not indented) — exit routes block
		if inRoutes && !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inRoutes = false
		}

		if inRoutes {
			// Parse "  key: value" lines
			colonIdx := strings.Index(trimmed, ":")
			if colonIdx <= 0 {
				continue
			}
			key := strings.TrimSpace(trimmed[:colonIdx])
			val := strings.TrimSpace(trimmed[colonIdx+1:])
			// Strip optional inline comment
			if ci := strings.Index(val, " #"); ci >= 0 {
				val = strings.TrimSpace(val[:ci])
			}
			if key != "" && val != "" {
				cfg.Routes[strings.ToLower(key)] = val
			}
		}
	}
	return cfg, nil
}

// SaveRoutingConfig persists the routing config back to disk.
func SaveRoutingConfig(appDir string, cfg *RoutingConfig) error {
	path := filepath.Join(appDir, "routing.yaml")
	var sb strings.Builder
	sb.WriteString("routes:\n")
	for k, v := range cfg.Routes {
		sb.WriteString(fmt.Sprintf("  %s: %s\n", k, v))
	}
	return os.WriteFile(path, []byte(sb.String()), 0644)
}

// FindModelIDForUseCase looks up the best model ID for a given use-case tag.
// It matches the stored alias/prefix against the provided list of model IDs.
// Returns empty string if no rule matches.
func (r *RoutingConfig) FindModelIDForUseCase(useCase string, modelIDs []string) string {
	if useCase == "" || r == nil {
		return ""
	}
	target, ok := r.Routes[strings.ToLower(useCase)]
	if !ok || target == "" {
		return ""
	}
	targetLower := strings.ToLower(target)
	// First pass: try ID prefix match
	for _, id := range modelIDs {
		if strings.HasPrefix(strings.ToLower(id), targetLower) {
			return id
		}
	}
	return ""
}

// WriteSampleRoutingConfig creates a documented routing.yaml in appDir
// if one doesn't already exist, so users know the format immediately.
func WriteSampleRoutingConfig(appDir string) error {
	path := filepath.Join(appDir, "routing.yaml")
	if _, err := os.Stat(path); err == nil {
		return nil // already exists — never overwrite user config
	}

	sample := `# Vraxter Use-Case Routing Configuration
# ===========================================
# Map a task type to the model you want to handle it.
# The value is a model ID prefix or alias fragment (case-insensitive).
# Leave a use-case commented out to fall back to priority-order routing.
#
# Available use-case tags (detected automatically from the query):
#   coding        - code generation, debugging, refactoring, tests
#   writing       - blog posts, documentation, emails, creative content
#   analysis      - data analysis, research, summarization, comparison
#   planning      - architecture design, implementation plans, roadmaps
#   math          - calculations, reasoning, proofs
#   general       - catch-all for everything not matched above
#
# Example — paste your model ID prefix (first 8 chars) from 'vraxter models list':
#
# routes:
#   coding: abc12345      # Use model whose ID starts with "abc12345"
#   writing: gpt-4o       # Use model whose alias matches "gpt-4o"
#   analysis: gemini
#   planning: claude
#   general: llama3

routes: {}
`
	return os.WriteFile(path, []byte(sample), 0644)
}
