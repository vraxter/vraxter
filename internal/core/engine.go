// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

// Package core provides the Engine type, which is now a thin compatibility
// wrapper around Orchestrator. New code should use Orchestrator directly.
package core

import (
	"context"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/skills"
	appcfg "github.com/vraxter/vraxter/internal/config"
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
	ChatRepo       *db.ChatRepository
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
	spatialSvc *services.SpatialService,
	verbose bool,
	appDir string, // needed to load routing.yaml
	requireSkillApproval bool,
) (*Engine, error) {
	// Load the use-case routing config from disk (won't fail if missing)
	routingCfg, err := appcfg.LoadRoutingConfig(appDir)
	if err != nil {
		// Best-effort: log and continue without routing overrides
		routingCfg = appcfg.DefaultRoutingConfig()
	}
	// Ensure a documented sample is written on first run
	_ = appcfg.WriteSampleRoutingConfig(appDir)

	orch, err := NewOrchestrator(store, crypto, reg, run, coder, spatialSvc, verbose, routingCfg.Routes, requireSkillApproval)
	if err != nil {
		return nil, err
	}
	return &Engine{
		orch:           orch,
		SpecialistRepo: orch.SpecialistRepo,
		ModelsRepo:     orch.ModelsRepo,
		ChatRepo:       orch.ChatRepo,
		Verbose:        orch.Verbose,
	}, nil
}

// JobManager returns the underlying JobManager for handling global events
func (e *Engine) JobManager() *JobManager {
	return e.orch.JobManager
}

// GetResolver exposes the orchestrator's resolver
func (e *Engine) GetResolver() *IntentResolver {
	return e.orch.Resolver
}

// ProcessRawIntent routes a raw text command through the LLM pipeline and executes resolved skills.
func (e *Engine) ProcessRawIntent(ctx context.Context, sessionID, text, specialistID, overrideModelID, sourceZone, sourceUser string) (<-chan llm.StreamEvent, error) {
	return e.orch.ProcessRawIntent(ctx, sessionID, text, specialistID, overrideModelID, sourceZone, sourceUser)
}

func (e *Engine) GetActiveModelID(sessionID string) string {
	return e.orch.GetActiveModelID(sessionID)
}

func (e *Engine) GetCodeGen() *CodeGenService {
	return e.orch.ExecEngine.CodeGen
}
