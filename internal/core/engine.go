// Package core provides the Engine type, which is now a thin compatibility
// wrapper around Orchestrator. New code should use Orchestrator directly.
package core

import (
	"context"

	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/security"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/internal/skills"
)

// LLMResponse is kept here for backward compatibility.
// Canonical definition now lives in pkg/types.
// TODO: remove this alias once all consumers import pkg/types.
type LLMResponse = interface{}

// Engine is a backward-compatible wrapper around Orchestrator.
// It exists so that cmd/vraxter/main.go and internal/server/handler.go
// do not need to be updated in this PR.
type Engine struct {
	orch *Orchestrator
	// Direct repo access for CLI sub-commands (e.g. specialists.go, models commands)
	SpecialistRepo *db.SpecialistRepository
	ModelsRepo     *db.ModelRepository
	// Expose Verbose for compatibility
	Verbose bool
}

// NewEngine creates an Engine backed by a fresh Orchestrator.
func NewEngine(
	store *db.Store,
	crypto *security.CryptoService,
	reg *skills.Registry,
	run *skills.Runner,
	coder *services.CoderService,
	verbose bool,
) (*Engine, error) {
	orch, err := NewOrchestrator(store, crypto, reg, run, coder, verbose)
	if err != nil {
		return nil, err
	}
	return &Engine{
		orch:           orch,
		SpecialistRepo: orch.SpecialistRepo,
		ModelsRepo:     orch.ModelsRepo,
		Verbose:        verbose,
	}, nil
}

// ProcessRawIntent delegates to the Orchestrator.
func (e *Engine) ProcessRawIntent(ctx context.Context, sessionID, text, specialistID string) (<-chan llm.StreamEvent, error) {
	return e.orch.ProcessRawIntent(ctx, sessionID, text, specialistID)
}
