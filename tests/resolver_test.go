// Package tests contains integration and unit tests for Vraxter core components.
// Tests here use the exported API surface of each package.
package tests

import (
	"context"
	"testing"

	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/pkg/types"
)

func TestIntentResolver_RegexMatch(t *testing.T) {
	reg := skills.NewRegistry()

	skill := types.SkillManifest{
		ID:         "test-currency",
		Name:       "Currency Converter",
		ParamRegex: `(?i)(?P<amount>\d+(?:\.\d+)?)\s+(?P<source>[a-zA-Z]{3})\s+(?:to|in)\s+(?P<target>[a-zA-Z]{3})`,
	}
	reg.Register(skill)

	resolver := core.NewIntentResolver(reg, llm.NewRouter(nil))

	query := "convert 500.5 USD to EUR"
	match := resolver.Resolve(context.Background(), query)

	if match.Confidence != 1.0 {
		t.Errorf("Expected confidence 1.0 for regex match, got %v", match.Confidence)
	}
	if match.SkillID != "test-currency" {
		t.Errorf("Expected skill ID 'test-currency', got %v", match.SkillID)
	}
	if match.Params["amount"] != "500.5" {
		t.Errorf("Expected param amount='500.5', got %v", match.Params["amount"])
	}
	if match.Params["source"] != "USD" {
		t.Errorf("Expected param source='USD', got %v", match.Params["source"])
	}
	if match.Params["target"] != "EUR" {
		t.Errorf("Expected param target='EUR', got %v", match.Params["target"])
	}
}

func TestIntentResolver_KeywordMatch(t *testing.T) {
	reg := skills.NewRegistry()

	skill := types.SkillManifest{
		ID:       "sys-info",
		Name:     "System Info",
		Keywords: []string{"cpu", "memory", "ram"},
		Tags:     []string{"system", "diagnostics"},
	}
	reg.Register(skill)

	resolver := core.NewIntentResolver(reg, llm.NewRouter(nil))

	query := "show me the ram and cpu usage for this system"
	match := resolver.Resolve(context.Background(), query)

	if match.Confidence <= 0 {
		t.Errorf("Expected positive confidence for keyword match, got %v", match.Confidence)
	}
	if match.SkillID != "sys-info" {
		t.Errorf("Expected skill ID 'sys-info', got %v", match.SkillID)
	}
	if match.Params["query"] != query {
		t.Errorf("Expected fallback query param, got %v", match.Params["query"])
	}
}
