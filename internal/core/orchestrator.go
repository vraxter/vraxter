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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/shlex"

	"github.com/google/uuid"
	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/prompts"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/internal/telemetry"
	"github.com/vraxter/vraxter/internal/utils"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

const (
	markerChat = "[VRAX_CHAT]"
	markerTool = "[VRAX_TOOL]"
	markerCode = "[VRAX_CODE]"
)

func sendEvent(ctx context.Context, ch chan<- llm.StreamEvent, evt llm.StreamEvent) bool {
	return interfaces.SendStreamEvent(ctx, ch, evt)
}

func isInternalControlCommand(trimmed string) bool {
	return strings.HasPrefix(trimmed, "!APPROVE_SKILL") ||
		strings.HasPrefix(trimmed, "!REJECT_SKILL") ||
		strings.HasPrefix(trimmed, "!EXECUTE_PLAN") ||
		strings.HasPrefix(trimmed, "!RUN_SKILL") ||
		strings.HasPrefix(trimmed, "!APPROVE_HOST_CMD") ||
		trimmed == "!CONTINUE_REASONING"
}

// FilterAllowedModels enforces the active isolation perimeter (airgap / strict_local / local_lan)
// uniformly across startup, cache refresh, and failover model sync.
func FilterAllowedModels(models []types.ModelConfig, isolationMode string, whitelistedIPs []string) []types.ModelConfig {
	mode := strings.ToLower(strings.TrimSpace(isolationMode))
	isRestricted := mode == "airgap" || mode == "strict_local" || mode == "local_lan"
	if !isRestricted {
		out := make([]types.ModelConfig, len(models))
		copy(out, models)
		return out
	}

	var allowedModels []types.ModelConfig
	for _, m := range models {
		pType := strings.ToLower(strings.TrimSpace(m.Provider))
		isCloud := pType == "openai" || pType == "anthropic" || pType == "google"
		if isCloud {
			slog.Info("Isolation Shield active: skipping cloud model", "mode", mode, "model_id", m.ID, "provider", m.Provider)
			continue
		}
		if (pType == "custom" || pType == "ollama") && strings.TrimSpace(m.BaseURL) != "" {
			allowed, err := utils.IsAllowedNetworkWithMode(m.BaseURL, mode, whitelistedIPs)
			if err != nil || !allowed {
				slog.Warn("Isolation Shield active: skipping model with unapproved endpoint", "mode", mode, "model_id", m.ID, "url", m.BaseURL, "err", err)
				continue
			}
		}
		allowedModels = append(allowedModels, m)
	}
	return allowedModels
}

// Orchestrator is the primary coordinator of Vraxter's request lifecycle.
// It owns the high-level flow: resolve intent → execute fast-path or delegate to LLM.
type Orchestrator struct {
	// Repositories
	ModelsRepo     *db.ModelRepository
	ChatRepo       *db.ChatRepository
	SpecialistRepo *db.SpecialistRepository
	MemoryRepo     *db.MemoryRepository
	ContextMgr     *ContextManager
	JobManager     *JobManager

	// Sub-controllers
	Resolver    *IntentResolver
	ExecEngine  *ExecutionEngine
	StreamCoord *StreamCoordinator
	Planner     *Planner
	UserRepo    *db.UserRepository
	MemorySvc   *services.MemoryService
	Swarm       *SwarmDispatcher
	AuditLedger *audit.Ledger

	// Performance Cache
	mu                sync.RWMutex
	cachedModels      []types.ModelConfig
	cachedSpecialists []types.Specialist
	cachedModelsCtx   string
	cachedSpecsCtx    string
	cacheExpiry       time.Time

	// Session State
	sessionOverrides map[string]string // sessionID -> modelID lock

	// Isolation Perimeter
	IsolationMode  string
	WhitelistedIPs []string

	// Deterministic Inference Defaults (wired into every CompletionRequest)
	DefaultTemperature *float64
	DefaultSeed        *int64
	DefaultMaxTokens   int

	Verbose bool
}

// NewOrchestrator bootstraps the full Orchestrator and all its sub-components.
func NewOrchestrator(
	store *db.Store,
	crypto *security.CryptoService,
	reg *skills.Registry,
	run *skills.Runner,
	coder *services.CoderService,
	verbose bool,
	routingOverrides map[string]string, // from routing.yaml: useCase → modelID
	requireSkillApproval bool,
	isolationMode string,
	whitelistedIPs []string,
) (*Orchestrator, error) {
	modelsRepo := db.NewModelRepository(store, crypto)
	chatRepo := db.NewChatRepository(store)
	specRepo := db.NewSpecialistRepository(store)
	memoryRepo := db.NewMemoryRepository(store)

	models, err := modelsRepo.GetActiveModels()
	if err != nil {
		return nil, fmt.Errorf("failed fetching models from local DB: %w", err)
	}

	allowedModels := FilterAllowedModels(models, isolationMode, whitelistedIPs)

	router := llm.NewRouter(allowedModels)
	bootedProviders := make(map[string]bool)
	for _, m := range allowedModels {
		if bootedProviders[m.ProviderID] {
			continue
		}
		adapter, err := llm.Create(m.Provider, m.APIKey, m.BaseURL)
		if err == nil {
			router.Register(m.ProviderID, adapter)
			bootedProviders[m.ProviderID] = true
		} else {
			slog.Warn("Failed to boot model provider", "provider_id", m.ProviderID, "type", m.Provider, "err", err)
		}
	}

	// Wire use-case routing overrides from routing.yaml
	for useCase, modelID := range routingOverrides {
		router.SetUseCaseOverride(useCase, modelID)
	}

	skillRepo := db.NewSkillRepository(store)
	codegen := &CodeGenService{
		Router:  router,
		Coder:   coder,
		Verbose: verbose,
	}
	jobMgr := NewJobManager()

	o := &Orchestrator{
		ModelsRepo:       modelsRepo,
		ChatRepo:         chatRepo,
		SpecialistRepo:   specRepo,
		MemoryRepo:       memoryRepo,
		ContextMgr:         NewContextManager(chatRepo),
		JobManager:         jobMgr,
		UserRepo:           db.NewUserRepository(store),
		Resolver:         NewIntentResolver(reg, router, skillRepo, specRepo),
		ExecEngine:       NewExecutionEngine(reg, run, coder, chatRepo, specRepo, jobMgr, codegen, verbose, requireSkillApproval),
		StreamCoord:      NewStreamCoordinator(router, verbose),
		Planner:          NewPlanner(chatRepo, router),
		MemorySvc:        services.NewMemoryService(memoryRepo, router),
		IsolationMode:      isolationMode,
		WhitelistedIPs:     whitelistedIPs,
		DefaultTemperature: llm.Float64Ptr(0.0),
		DefaultMaxTokens:   4096,
		Verbose:            verbose,
		sessionOverrides:   make(map[string]string),
	}

	o.ExecEngine.GetActiveModelConfigDelegate = o.GetActiveModelConfig
	o.ExecEngine.HostExecGuard = security.NewHostExecGuard(security.HostExecOptions{
		Enabled:        false,
		IsolationMode:  isolationMode,
		WhitelistedIPs: whitelistedIPs,
	})
	o.Swarm = NewSwarmDispatcher(o.ExecEngine, jobMgr, o.Resolver)

	// Register System Skills for Semantic Matching/Fast Path
	reg.Register(types.SkillManifest{
		ID:          "vraxter-activate-model",
		Name:        "Activate Model",
		Description: "Lock a specific model for the session. Usage: activate <model_id>",
		ParamRegex:  `activate\s+(?:the\s+)?(?:model\s+)?(?P<model_id>[a-zA-Z0-9\-\._]+)`,
		IsOfficial:  true,
	})
	reg.Register(types.SkillManifest{
		ID:          "vraxter-restore-model",
		Name:        "Restore Model",
		Description: "Unlock model routing and restore automatic selection. Usage: restore model",
		ParamRegex:  `(restore|auto)\s+model`,
		IsOfficial:  true,
	})

	reg.Register(types.SkillManifest{
		ID:          "vraxter-see",
		Name:        "See",
		Description: "Analyze and describe an image file using the active vision model.",
		ParamRegex:  `(?:see|look|analyze|describe)\s+(?:the\s+)?(?:image\s+|picture\s+|photo\s+)?(?P<path>[^\s]+\.(png|jpg|jpeg|webp|gif))(?:\s+(?P<prompt>.*))?`,
		IsOfficial:  true,
	})
	reg.Register(types.SkillManifest{
		ID:          "vraxter-hear",
		Name:        "Hear",
		Description: "Transcribe an audio file using the active audio model or API.",
		ParamRegex:  `(?:hear|listen|transcribe)\s+(?:the\s+)?(?:audio\s+|voice\s+|sound\s+)?(?P<path>[^\s]+\.(mp3|wav|ogg|m4a|flac))(?:\s+(?P<prompt>.*))?`,
		IsOfficial:  true,
	})
	reg.Register(types.SkillManifest{
		ID:          "vraxter-talk",
		Name:        "Talk",
		Description: "Synthesize speech from text and route it to a target spatial zone for playback.",
		ParamRegex:  `(?:talk|speak|say|synthesize|reply)\s+"?(?P<text>[^"]+)"?(?:\s+(?:in|to|at)\s+(?:the\s+)?(?P<target_zone>[a-zA-Z0-9_-]+))?`,
		IsOfficial:  true,
	})

	// Filesystem Tools — Semantic Matching Registration
	reg.Register(types.SkillManifest{
		ID:          "vraxter-read-file",
		Name:        "Read File",
		Description: "Read the contents of a local file from the workspace or filesystem. Returns the file text content.",
		Keywords:    []string{"read", "file", "cat", "show", "content", "contents", "open", "view"},
		Tags:        []string{"filesystem", "io", "workspace"},
		Examples:    []string{"read this file /path/to/file", "show me the contents of config.yaml", "read this file", "what does this file contain"},
		ParamRegex:  `(?:read|cat|show|open|view)\s+(?:the\s+)?(?:file\s+|contents?\s+(?:of\s+)?)?(?P<path>[^\s]+\.[a-zA-Z0-9]+)`,
		IsOfficial:  true,
	})
	reg.Register(types.SkillManifest{
		ID:          "vraxter-list-dir",
		Name:        "List Directory",
		Description: "List the contents of a local directory to explore files and folders in the workspace.",
		Keywords:    []string{"list", "ls", "dir", "directory", "folder", "files", "tree", "explore"},
		Tags:        []string{"filesystem", "io", "workspace"},
		Examples:    []string{"list the files in /home", "what's in the src directory", "ls /tmp", "list files in workspace", "explore the workspace"},
		ParamRegex:  `(?:list|ls|dir|explore)\s+(?:the\s+)?(?:files?\s+(?:in|from)\s+|contents?\s+(?:of\s+)?|directory\s+)?(?P<path>(?:\/|~|\.)[^\s]*)`,
		IsOfficial:  true,
	})
	reg.Register(types.SkillManifest{
		ID:          "vraxter-patch-code",
		Name:        "Patch Code",
		Description: "Perform an exact search-and-replace edit on a target file. Requires path, search block, and replace block.",
		Keywords:    []string{"patch", "replace", "edit", "modify", "fix", "cambiar", "reemplazar", "editar", "refactor"},
		Tags:        []string{"filesystem", "code", "refactor"},
		Examples:    []string{"patch this file", "replace this code block", "edit the function", "fix this line"},
		IsOfficial:  true,
	})

	o.ExecEngine.SwarmDelegate = o.DelegateSwarmTask
	o.ExecEngine.ActivateModelDelegate = o.SetSessionOverride
	o.ExecEngine.RestoreModelDelegate = o.ClearSessionOverride
	o.ExecEngine.ResolveSpecialistDelegate = o.Resolver.FindSpecialistSemantically
	o.ExecEngine.OnSpecialistChange = o.InvalidateCache

	if ledger, lErr := audit.NewLedger(store, "", ""); lErr == nil {
		o.SetAuditLedger(ledger)
		if audit.DefaultLedger() == nil {
			audit.SetDefaultLedger(ledger)
		}
	}
	return o, nil
}

// SetAuditLedger attaches a forensic audit ledger to the orchestrator, execution engine, and WASM runner.
func (o *Orchestrator) SetAuditLedger(l *audit.Ledger) {
	if o == nil {
		return
	}
	o.mu.Lock()
	o.AuditLedger = l
	o.mu.Unlock()
	if o.ExecEngine != nil {
		o.ExecEngine.AuditLedger = l
		if o.ExecEngine.Runner != nil {
			o.ExecEngine.Runner.SetAuditLedger(l)
		}
	}
}

func (o *Orchestrator) recordAudit(ctx context.Context, sessionID, actor, eventType string, payload interface{}) {
	o.mu.RLock()
	l := o.AuditLedger
	o.mu.RUnlock()
	if l == nil {
		l = audit.DefaultLedger()
	}
	if l == nil {
		return
	}
	_, _ = l.Append(ctx, audit.AuditEventInput{
		SessionID: sessionID,
		Actor:     actor,
		EventType: eventType,
		Payload:   payload,
	})
}

// InvalidateCache clears the cache expiry to force an immediate refresh on the next query.
func (o *Orchestrator) InvalidateCache() {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.cacheExpiry = time.Time{}
}

// refreshCache keeps in-memory metadata fresh (max 30s staleness).
func (o *Orchestrator) refreshCache() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if time.Now().Before(o.cacheExpiry) {
		return nil
	}

	isoMode := o.IsolationMode
	wlCopy := append([]string(nil), o.WhitelistedIPs...)

	rawModels, err := o.ModelsRepo.GetAllModelsPublic()
	if err != nil {
		return err
	}
	models := FilterAllowedModels(rawModels, isoMode, wlCopy)

	specs, err := o.SpecialistRepo.GetAllSpecialists()
	if err != nil {
		return err
	}

	o.cachedModels = models
	o.cachedSpecialists = specs

	if activeModels, err := o.ModelsRepo.GetActiveModels(); err == nil && len(activeModels) > 0 && o.StreamCoord != nil && o.StreamCoord.Router != nil {
		allowedActive := FilterAllowedModels(activeModels, isoMode, wlCopy)
		o.StreamCoord.Router.SyncActiveModels(allowedActive)
	}

	// Rebuild model/spec context strings
	var mCtx strings.Builder
	for _, m := range models {
		status := "READY"
		if !m.IsConfigured {
			status = "MISSING_CONFIG (Provider setup required)"
		} else if !m.IsActive {
			status = "INACTIVE"
		}
		mCtx.WriteString(fmt.Sprintf("- ID: '%s' | Provider: %s | Model: %s | Status: %s | Capabilities: [%s] | Context: %d\n",
			m.ID, m.Provider, m.Model, status, m.Capabilities, m.ContextWindow))
	}
	o.cachedModelsCtx = mCtx.String()
	if o.cachedModelsCtx == "" {
		o.cachedModelsCtx = "No models configured.\n"
	}

	var sCtx strings.Builder
	for _, s := range specs {
		sCtx.WriteString(fmt.Sprintf("- Specialist: %s\n  Expertise: %s\n", s.Name, s.Expertise))
	}
	o.cachedSpecsCtx = strings.TrimSpace(sCtx.String())
	if len(specs) == 0 {
		o.cachedSpecsCtx = "None."
	}

	o.cacheExpiry = time.Now().Add(30 * time.Second)
	return nil
}

func (o *Orchestrator) IdentifyIntent(ctx context.Context, text string) (types.IntentMatch, error) {
	if len(text) == 0 {
		return types.IntentMatch{}, nil
	}
	if isInternalControlCommand(strings.TrimSpace(text)) {
		return types.IntentMatch{}, nil
	}
	prefix := string(text[0])
	rawPayload := text[1:]

	switch prefix {
	case types.SkillExecutionPrefix:
		parts := strings.SplitN(rawPayload, " ", 2)
		skillID := parts[0]
		query := ""
		if len(parts) > 1 {
			query = parts[1]
		}

		if o.Verbose {
			slog.Info("Skill execution detected", "skill", skillID+" "+query)
		}
		return types.IntentMatch{
			Type:       types.IntentTypeSkill,
			ID:         skillID,
			Confidence: 1.0,
			Params:     map[string]interface{}{"query": query},
			Args:       []string{},
		}, nil

	case types.CommandExecutionPrefix:
		if security.ContainsShellMetacharacters(rawPayload) {
			return types.IntentMatch{}, fmt.Errorf("%w", security.ErrInjectionDetected)
		}
		args, err := shlex.Split(rawPayload)
		if err != nil || len(args) == 0 {
			return types.IntentMatch{}, fmt.Errorf("failed to parse command: %v", err)
		}

		return types.IntentMatch{
			Type:       types.IntentTypeCommand,
			ID:         args[0],
			Args:       args[1:],
			Confidence: 1.0,
			Params: map[string]interface{}{
				"_raw_payload": rawPayload,
			},
		}, nil
	}

	return types.IntentMatch{}, nil
}

// resolveMention scans for structural @mention signals in the query.
// This is language-agnostic: it only matches the @ prefix, not any natural language verbs.
// Returns the match and the remaining task text (everything after the @mention).
func (o *Orchestrator) resolveMention(query string) (types.IntentMatch, string) {
	lowerQuery := strings.ToLower(query)

	// Check for @Vraxter (return-control signal)
	if idx := strings.Index(lowerQuery, "@vraxter"); idx != -1 {
		remaining := strings.TrimSpace(query[idx+len("@vraxter"):])
		return types.IntentMatch{
			Type:       types.IntentTypeReturnControl,
			ID:         "vraxter-return-control",
			Confidence: types.SignalReturnControl.Confidence(),
			Signal:     types.SignalReturnControl,
			Params:     map[string]interface{}{"task": remaining},
		}, remaining
	}

	// Check for @SpecialistName
	if o.SpecialistRepo != nil {
		allSpecs, err := o.SpecialistRepo.GetAllSpecialists()
		if err == nil {
			for _, s := range allSpecs {
				mention := "@" + strings.ToLower(s.Name)
				if idx := strings.Index(lowerQuery, mention); idx != -1 {
					remaining := strings.TrimSpace(query[idx+len(mention):])
					if remaining == "" {
						remaining = query[:idx]
						remaining = strings.TrimSpace(remaining)
					}
					return types.IntentMatch{
						Type:       types.IntentTypeSpecialist,
						ID:         s.ID,
						Confidence: types.SignalExplicitMention.Confidence(),
						Signal:     types.SignalExplicitMention,
						Params:     map[string]interface{}{"task": remaining},
					}, remaining
				}
			}
		}
	}

	return types.IntentMatch{}, ""
}

// resolveSpecialist searches for a specialist by ID or Name (case-insensitive, ignoring leading @).
func (o *Orchestrator) resolveSpecialist(idOrName string) (*types.Specialist, error) {
	clean := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(idOrName, "@")))
	if clean == "" || clean == "supervisor" || clean == "vraxter" {
		return nil, fmt.Errorf("no specialist")
	}
	if o.SpecialistRepo != nil {
		if spec, err := o.SpecialistRepo.GetSpecialist(clean); err == nil && spec != nil {
			return spec, nil
		}
		specs, err := o.SpecialistRepo.GetAllSpecialists()
		if err == nil {
			for _, s := range specs {
				if strings.ToLower(s.Name) == clean || strings.ToLower(s.ID) == clean {
					return &s, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("specialist '%s' not found", idOrName)
}

// detectPlanIntent determines if a query is a planning request (/plan or AMEND PLAN:)
// and resolves any explicit or contextual lead specialist associated with it.
// Returns (isPlan, cleanQuery, leadSpecialist).
func (o *Orchestrator) detectPlanIntent(text string, currentSpecID string) (bool, string, *types.Specialist) {
	trimmed := strings.TrimSpace(text)
	lower := strings.ToLower(trimmed)

	isAmend := strings.HasPrefix(lower, "amend plan:") || strings.Contains(lower, "amend plan:")
	isPlanCmd := false

	if isAmend {
		isPlanCmd = true
	} else {
		for _, token := range strings.Fields(trimmed) {
			lowToken := strings.ToLower(token)
			if lowToken == "/plan" || strings.HasPrefix(lowToken, "/plan@") || strings.HasPrefix(lowToken, "/plan:") {
				isPlanCmd = true
				break
			}
		}
	}

	if !isPlanCmd {
		return false, text, nil
	}

	var leadSpec *types.Specialist
	workingText := trimmed

	// 1. Check for explicit @mention in text
	if o.SpecialistRepo != nil {
		specs, err := o.SpecialistRepo.GetAllSpecialists()
		if err == nil {
			for _, s := range specs {
				mentionName := "@" + strings.ToLower(s.Name)
				mentionID := "@" + strings.ToLower(s.ID)

				lowerWorking := strings.ToLower(workingText)
				if idx := strings.Index(lowerWorking, mentionName); idx != -1 {
					leadSpec = &s
					workingText = strings.TrimSpace(workingText[:idx] + " " + workingText[idx+len(mentionName):])
					break
				}
				if idx := strings.Index(lowerWorking, mentionID); idx != -1 {
					leadSpec = &s
					workingText = strings.TrimSpace(workingText[:idx] + " " + workingText[idx+len(mentionID):])
					break
				}
			}
		}
	}

	// 2. If no explicit @mention, check if we are currently inside a specialist conversation
	if leadSpec == nil && currentSpecID != "" {
		if spec, err := o.resolveSpecialist(currentSpecID); err == nil && spec != nil {
			leadSpec = spec
		}
	}

	// 3. Strip /plan or AMEND PLAN: command prefixes/tokens
	if isAmend {
		idx := strings.Index(strings.ToLower(workingText), "amend plan:")
		if idx != -1 {
			workingText = strings.TrimSpace(workingText[:idx] + " " + workingText[idx+len("amend plan:"):])
		}
	} else {
		var cleanTokens []string
		for _, token := range strings.Fields(workingText) {
			lowToken := strings.ToLower(token)
			if lowToken == "/plan" || strings.HasPrefix(lowToken, "/plan@") || strings.HasPrefix(lowToken, "/plan:") {
				continue
			}
			cleanTokens = append(cleanTokens, token)
		}
		workingText = strings.Join(cleanTokens, " ")
	}

	cleanQuery := strings.TrimSpace(workingText)
	return true, cleanQuery, leadSpec
}

func (o *Orchestrator) GetActiveModelID(sessionID string) string {
	if sessionID != "" {
		o.mu.RLock()
		lock, hasLock := o.sessionOverrides[sessionID]
		o.mu.RUnlock()
		if hasLock && lock != "" {
			if cfg, _, err := o.StreamCoord.Router.GetProviderByID(lock); err == nil {
				if cfg.Alias != "" {
					return cfg.Alias
				}
				if cfg.Model != "" {
					return cfg.Model
				}
			}
			return lock
		}
	}

	ordered := o.StreamCoord.Router.GetOrderedProviders()
	if len(ordered) == 0 && o.ModelsRepo != nil {
		if activeModels, err := o.ModelsRepo.GetActiveModels(); err == nil && len(activeModels) > 0 {
			o.mu.RLock()
			isoMode := o.IsolationMode
			wlCopy := append([]string(nil), o.WhitelistedIPs...)
			o.mu.RUnlock()
			allowedActive := FilterAllowedModels(activeModels, isoMode, wlCopy)
			if len(allowedActive) > 0 {
				o.StreamCoord.Router.SyncActiveModels(allowedActive)
				ordered = o.StreamCoord.Router.GetOrderedProviders()
			}
		}
	}
	if len(ordered) > 0 {
		if ordered[0].Config.Alias != "" {
			return ordered[0].Config.Alias
		}
		return ordered[0].Config.Model
	}
	return "unknown"
}

func (o *Orchestrator) GetFailoverModelID(sessionID string) string {
	active := o.GetActiveModelID(sessionID)
	ordered := o.StreamCoord.Router.GetOrderedProviders()
	if len(ordered) == 0 && o.ModelsRepo != nil {
		if activeModels, err := o.ModelsRepo.GetActiveModels(); err == nil && len(activeModels) > 0 {
			o.mu.RLock()
			isoMode := o.IsolationMode
			wlCopy := append([]string(nil), o.WhitelistedIPs...)
			o.mu.RUnlock()
			allowedActive := FilterAllowedModels(activeModels, isoMode, wlCopy)
			if len(allowedActive) > 0 {
				o.StreamCoord.Router.SyncActiveModels(allowedActive)
				ordered = o.StreamCoord.Router.GetOrderedProviders()
			}
		}
	}

	cleanActive := strings.Trim(strings.ToLower(active), "'\" ")
	for _, p := range ordered {
		cleanModel := strings.Trim(strings.ToLower(p.Config.Model), "'\" ")
		cleanAlias := strings.Trim(strings.ToLower(p.Config.Alias), "'\" ")
		cleanID := strings.Trim(strings.ToLower(p.Config.ID), "'\" ")

		if cleanModel != cleanActive && cleanAlias != cleanActive && cleanID != cleanActive {
			if p.Config.Alias != "" {
				return p.Config.Alias
			}
			return p.Config.Model
		}
	}
	return "none"
}

func (o *Orchestrator) GetActiveModelConfig(sessionID string) (types.ModelConfig, error) {
	modelID := o.GetActiveModelID(sessionID)
	cleanTarget := strings.Trim(strings.ToLower(modelID), "'\" ")
	// Try to match the exact alias or model string from the ordered providers
	for _, p := range o.StreamCoord.Router.GetOrderedProviders() {
		cleanAlias := strings.Trim(strings.ToLower(p.Config.Alias), "'\" ")
		cleanModel := strings.Trim(strings.ToLower(p.Config.Model), "'\" ")
		cleanID := strings.Trim(strings.ToLower(p.Config.ID), "'\" ")
		if cleanAlias == cleanTarget || cleanModel == cleanTarget || cleanID == cleanTarget {
			return p.Config, nil
		}
	}
	return types.ModelConfig{}, fmt.Errorf("no provider found matching active model '%s'", modelID)
}

func (o *Orchestrator) resolveConversationContext(sessionID, specialistID string) string {
	conversationID := sessionID
	if specialistID != "" {
		c, err := o.ChatRepo.FindLatestConversationBySpecialist(specialistID)
		if err == nil && c != nil {
			conversationID = c.ID
		} else {
			conversationID = uuid.New().String()
			o.ChatRepo.CreateConversation(&types.Conversation{
				ID:           conversationID,
				Title:        "Specialist Chat",
				SpecialistID: specialistID,
			})
		}
	} else {
		c, err := o.ChatRepo.FindConversation(conversationID)
		if err != nil || c == nil {
			o.ChatRepo.CreateConversation(&types.Conversation{
				ID:    conversationID,
				Title: "Project Context Topic",
			})
		}
	}
	return conversationID
}

func (o *Orchestrator) gatherPromptContext(ctx context.Context, text, sourceUser string) (string, string, int) {
	var userProfilePool, memoryContextPool string
	var memoryCount int
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		var user *db.UserProfile
		var err error
		if sourceUser != "" {
			user, err = o.UserRepo.GetUserByName(ctx, sourceUser)
		}
		if user == nil || err != nil {
			user, err = o.UserRepo.GetDefaultUser(ctx)
		}

		if err == nil && user != nil {
			userProfilePool = fmt.Sprintf("User: %s. expertise: %s.", user.Name, user.Expertise)
		}
	}()
	go func() {
		defer wg.Done()
		ragCtx, cancel := context.WithTimeout(ctx, 8*time.Second) // Strict RAG timeout
		defer cancel()
		memories, _ := o.MemorySvc.SearchMemories(ragCtx, text, 3)
		memoryCount = len(memories)
		if len(memories) > 0 {
			var b strings.Builder
			for _, m := range memories {
				b.WriteString("- " + m.Content + "\n")
			}
			memoryContextPool = b.String()
		}
	}()
	wg.Wait()
	return userProfilePool, memoryContextPool, memoryCount
}

func (o *Orchestrator) resolveExecutionModel(ctx context.Context, text, sessionID, overrideModelID string, out chan<- llm.StreamEvent) (types.ModelConfig, error) {
	var modelCfg types.ModelConfig

	// Priority: Explicit request override > Session-level lock
	if overrideModelID == "" {
		o.mu.RLock()
		if lock, ok := o.sessionOverrides[sessionID]; ok {
			overrideModelID = lock
		}
		o.mu.RUnlock()
	}

	if overrideModelID != "" {
		cfg, _, err := o.StreamCoord.Router.GetProviderByID(overrideModelID)
		if err == nil {
			modelCfg = cfg
		}
	}

	if modelCfg.ID == "" {
		useCase := llm.ClassifyUseCase(text)
		
		// Detection: check if the preferred model is actually available
		preferredCfg, _, _ := o.StreamCoord.Router.GetProviderForUseCase(useCase)
		ordered := o.StreamCoord.Router.GetOrderedProvidersForUseCase(useCase)
		
		if len(ordered) == 0 {
			return modelCfg, fmt.Errorf("no LLM models configured")
		}

		modelCfg = ordered[0].Config
		
		// Fallback notification
		if preferredCfg.ID != "" && modelCfg.ID != preferredCfg.ID {
			msg := fmt.Sprintf("⚠️ Use-case model '%s' unavailable. Falling back to '%s'.", preferredCfg.Alias, modelCfg.Alias)
			select {
			case out <- llm.StreamEvent{Type: llm.EventTypeStatus, Content: msg}:
			default:
			}
		}
	}
	return modelCfg, nil
}

// ProcessRawIntent routes a raw text command through the LLM pipeline and executes resolved skills.
func (o *Orchestrator) ProcessRawIntent(ctx context.Context, conversationID, text, specialistID, overrideModelID, sourceZone, sourceUser string) (<-chan llm.StreamEvent, error) {
	out := make(chan llm.StreamEvent)
	intentID := uuid.New().String()
	ctx, _, span := telemetry.EnsureTraceID(ctx, "orchestrator.ProcessRawIntent")

	go func() {
		defer close(out)
		defer span.End()
		reqStart := time.Now()
		finalStatus := "completed"
		var resolvedModelID string

		// 1. Initial Status message to confirm responsiveness
		select {
		case out <- llm.StreamEvent{Type: llm.EventTypeStatus, Content: "Analyzing query & gathering context..."}:
		case <-ctx.Done():
			return
		}

		// Normalize specialist context
		if specialistID != "" && specialistID != "supervisor" && specialistID != "vraxter" {
			if spec, err := o.resolveSpecialist(specialistID); err == nil && spec != nil {
				specialistID = spec.ID
			}
		} else if specialistID == "supervisor" || specialistID == "vraxter" {
			specialistID = ""
		}

		// Resolve conversation early
		conversationID := o.resolveConversationContext(conversationID, specialistID)
		if overrideModelID != "" {
			_ = o.SetSessionOverride(conversationID, overrideModelID)
		}

		actor := "user"
		if authActor, ok := audit.AuthenticatedActorFromContext(ctx); ok {
			actor = authActor
		} else if specialistID != "" {
			actor = "specialist:" + specialistID
		} else if strings.TrimSpace(sourceUser) != "" {
			actor = strings.TrimSpace(sourceUser)
		}

		ctx = audit.WithSessionID(ctx, conversationID)
		if _, hasAuth := audit.AuthenticatedActorFromContext(ctx); !hasAuth {
			ctx = audit.WithActor(ctx, actor)
		}

		o.mu.RLock()
		isoMode := o.IsolationMode
		o.mu.RUnlock()

		inputSum := sha256.Sum256([]byte(text))
		inputHash := hex.EncodeToString(inputSum[:])

		o.recordAudit(ctx, conversationID, actor, audit.EventRequestReceived, map[string]interface{}{
			"input_sha256":   inputHash,
			"input_length":   len(text),
			"origin":         audit.OriginFromContext(ctx),
			"isolation_mode": isoMode,
			"specialist_id":  specialistID,
			"source_zone":    sourceZone,
		})

		defer func() {
			if resolvedModelID == "" {
				resolvedModelID = o.GetActiveModelID(conversationID)
			}
			o.recordAudit(ctx, conversationID, actor, audit.EventResponseEmitted, map[string]interface{}{
				"model_id":         resolvedModelID,
				"status":           finalStatus,
				"total_latency_ms": time.Since(reqStart).Milliseconds(),
			})
		}()

		trimmedText := strings.TrimSpace(text)

		// CONTROL COMMANDS (HITL / Plan / Direct Run):
		// Handled BEFORE @mention, /plan, and IdentifyIntent ("!") so they are never hijacked
		// as ordinary skills or specialist mentions, and never hold o.mu across execution.
		isControlContinuation := false

		if strings.HasPrefix(trimmedText, "!RUN_SKILL") {
			skillID := strings.TrimSpace(strings.TrimPrefix(trimmedText, "!RUN_SKILL"))
			if !sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n▶️ **Running skill:** %s\n", skillID)}) {
				return
			}
			result := o.ExecEngine.HandleStandardSkill(ctx, out, types.ToolCall{SkillID: skillID}, skillID)
			sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n✅ **Result:**\n%s\n", result)})
			return
		}

		if strings.HasPrefix(trimmedText, "!APPROVE_HOST_CMD") {
			payloadStr := strings.TrimSpace(strings.TrimPrefix(trimmedText, "!APPROVE_HOST_CMD"))
			var req struct {
				Command string                 `json:"command"`
				Args    []string               `json:"args"`
				Params  map[string]interface{} `json:"params"`
			}
			if err := json.Unmarshal([]byte(payloadStr), &req); err != nil {
				sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n❌ Error: Invalid payload for host command approval.\n"})
				return
			}
			cmdName := req.Command
			cmdArgs := req.Args
			if cmdName == "" && req.Params != nil {
				cmdName, _ = req.Params["command"].(string)
				if rawArgs, ok := req.Params["args"].([]interface{}); ok {
					for _, a := range rawArgs {
						if s, ok := a.(string); ok {
							cmdArgs = append(cmdArgs, s)
						}
					}
				}
			}
			o.recordAudit(ctx, conversationID, actor, audit.EventHITLDecision, map[string]interface{}{
				"decision":    "APPROVED",
				"target_type": "host_cmd",
				"command":     cmdName,
			})
			o.ExecEngine.RunFastPath(ctx, out, types.IntentMatch{
				Type:       types.IntentTypeCommand,
				ID:         cmdName,
				Args:       cmdArgs,
				Confidence: 1.0,
				Params:     map[string]interface{}{"_hitl_approved": true},
			}, conversationID)
			return
		}

		if strings.HasPrefix(trimmedText, "!EXECUTE_PLAN") {
			planJSON := strings.TrimSpace(strings.TrimPrefix(trimmedText, "!EXECUTE_PLAN"))
			var plan Plan
			if err := json.Unmarshal([]byte(planJSON), &plan); err == nil {
				jobID := o.JobManager.SpawnDaemon("Plan Execution: "+plan.Goal, func() (string, error) {
					bgCtx := context.Background()
					var finalOutput strings.Builder

					for i, phase := range plan.Phases {
						// Broadcast progress so TUI sees the phase shifts
						o.JobManager.Broadcast(SystemEvent{
							Type:      EventSwarmProgress,
							Title:     "Executing Plan",
							Payload:   fmt.Sprintf("Running Phase %d: %s", i+1, phase.Title),
							Timestamp: time.Now(),
						})

						// Swallow stream tokens locally and synchronize drain completion via WaitGroup
						dummyOut := make(chan llm.StreamEvent, 100)
						var phaseOutput strings.Builder
						var drainWG sync.WaitGroup
						drainWG.Add(1)
						go func() {
							defer drainWG.Done()
							for evt := range dummyOut {
								if evt.Type == llm.EventTypeToken {
									phaseOutput.WriteString(evt.Content)
								}
							}
						}()

						phaseQuery := fmt.Sprintf("TASK: %s\nDESCRIPTION: %s", phase.Title, phase.Description)
						specID := phase.Specialist
						if specID == "supervisor" || specID == "vraxter" {
							specID = ""
						}
						_ = o.executeSubTask(bgCtx, phaseQuery, specID, dummyOut)
						close(dummyOut)
						drainWG.Wait()

						finalOutput.WriteString(fmt.Sprintf("=== Phase %d: %s ===\n%s\n\n", i+1, phase.Title, phaseOutput.String()))
					}

					return finalOutput.String(), nil
				})

				sendEvent(ctx, out, llm.StreamEvent{
					Type:    llm.EventTypeToken,
					Content: fmt.Sprintf("\n⚡ Spawned Plan Execution Daemon (Job ID: %s). The plan is now running in the background.\n", jobID),
				})
			} else {
				sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeError, Err: err})
			}
			return
		}

		if strings.HasPrefix(trimmedText, "!APPROVE_SKILL") {
			payloadStr := strings.TrimSpace(strings.TrimPrefix(trimmedText, "!APPROVE_SKILL"))
			var call types.ToolCall
			if err := json.Unmarshal([]byte(payloadStr), &call); err == nil {
				o.recordAudit(ctx, conversationID, actor, audit.EventHITLDecision, map[string]interface{}{
					"decision":    "APPROVED",
					"target_type": "skill",
					"skill_id":    call.SkillID,
				})
				if !sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n✅ **Skill Execution Approved:** `%s`\n", call.SkillID)}) {
					return
				}
				// Execute with per-invocation HITL approval (handles both built-in guarded mutations and WASM skills)
				var result string
				if _, isBuiltIn := BuiltInCommands[call.SkillID]; isBuiltIn {
					result = o.ExecEngine.DispatchApprovedToolPayload(ctx, out, call, conversationID)
				} else {
					result = o.ExecEngine.HandleStandardSkillWithBypass(ctx, out, call, call.SkillID, true)
				}

				// Save the tool output so the LLM context is updated
				o.ChatRepo.SaveMessage(&types.Message{
					ID:             uuid.New().String(),
					ConversationID: conversationID,
					Role:           "assistant",
					Content:        fmt.Sprintf("[%s]: %s", call.SkillID, result),
					Timestamp:      time.Now(),
				})

				// Continue reasoning in standard LLM loop without re-entering fast paths
				trimmedText = "!CONTINUE_REASONING"
				isControlContinuation = true
			} else {
				sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n❌ Error: Invalid payload for skill approval.\n"})
				return
			}
		}

		if !isControlContinuation && strings.HasPrefix(trimmedText, "!REJECT_SKILL") {
			payloadStr := strings.TrimSpace(strings.TrimPrefix(trimmedText, "!REJECT_SKILL"))
			var call types.ToolCall
			if err := json.Unmarshal([]byte(payloadStr), &call); err == nil {
				o.recordAudit(ctx, conversationID, actor, audit.EventHITLDecision, map[string]interface{}{
					"decision":    "REJECTED",
					"target_type": "skill",
					"skill_id":    call.SkillID,
				})
				if !sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n❌ **Skill Execution Rejected:** `%s`\n", call.SkillID)}) {
					return
				}

				// Save the rejection feedback so the LLM context is updated
				o.ChatRepo.SaveMessage(&types.Message{
					ID:             uuid.New().String(),
					ConversationID: conversationID,
					Role:           "assistant",
					Content:        fmt.Sprintf("[%s]: SYSTEM ERROR: The execution of this skill was REJECTED by the user due to Security Policies. You MUST try an alternative approach or ask the user for permission.", call.SkillID),
					Timestamp:      time.Now(),
				})

				// Continue reasoning in standard LLM loop without re-entering fast paths
				trimmedText = "!CONTINUE_REASONING"
				isControlContinuation = true
			} else {
				sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n❌ Error: Invalid payload for skill rejection.\n"})
				return
			}
		}

		if !isControlContinuation {
			// FAST PATH -1: Architecture Planning & Amendments (/plan, AMEND PLAN:)
			// Evaluated before mention delegation so that `/plan @spec` and `@spec /plan` build plans rather than bare chat.
			if isPlan, planQuery, leadSpec := o.detectPlanIntent(trimmedText, specialistID); isPlan {
				o.ChatRepo.SaveMessage(&types.Message{
					ID: intentID, ConversationID: conversationID,
					Role: "user", Content: text, Timestamp: time.Now(),
				})

				if planQuery == "" {
					sendEvent(ctx, out, llm.StreamEvent{
						Type:    llm.EventTypeToken,
						Content: "\n⚠️ Please provide the task details to generate a plan. Usage: `/plan [@specialist] <task>`\n",
					})
					return
				}

				// Focus specialist in UI if designated
				if leadSpec != nil {
					if overrideModelID == "" && leadSpec.ModelID != "" {
						overrideModelID = leadSpec.ModelID
					}
					if !sendEvent(ctx, out, llm.StreamEvent{
						Type:    llm.EventTypeSpecialistResult,
						Content: fmt.Sprintf("%s|%s|", leadSpec.ID, leadSpec.Name),
					}) {
						return
					}
				}

				if err := o.refreshCache(); err != nil {
					slog.Warn("Metadata cache refresh encountered issues", "error", err)
				}

				o.mu.RLock()
				specsCtxStr := o.cachedSpecsCtx
				o.mu.RUnlock()

				daemonTitle := "Architecture Planner"
				spawnMsg := "\n⚡ Spawned Architecture Planner Daemon (Job ID: %s). You will be notified when the plan is ready.\n"
				targetSpecName := ""
				if leadSpec != nil {
					targetSpecName = leadSpec.Name
					daemonTitle = fmt.Sprintf("Architecture Planner (@%s)", leadSpec.Name)
					spawnMsg = fmt.Sprintf("\n⚡ Spawned Architecture Planner Daemon led by @%s (Job ID: %%s). You will be notified when the plan is ready.\n", leadSpec.Name)
				}

				jobID := o.JobManager.SpawnDaemon(daemonTitle, func() (string, error) {
					planCtx := context.Background()
					plan, err := o.Planner.BuildPlan(planCtx, planQuery, specsCtxStr, overrideModelID, targetSpecName)
					if err != nil {
						return "", err
					}
					planJSON, _ := json.Marshal(plan)

					go func() {
						time.Sleep(50 * time.Millisecond)
						o.JobManager.Broadcast(SystemEvent{
							Type:      EventPlanProposed,
							JobID:     "plan-" + uuid.New().String(),
							Title:     "Plan Proposed",
							Payload:   string(planJSON),
							Timestamp: time.Now(),
						})
					}()

					return "", nil
				})

				sendEvent(ctx, out, llm.StreamEvent{
					Type:    llm.EventTypeToken,
					Content: fmt.Sprintf(spawnMsg, jobID),
				})
				return
			}

			// FAST PATH 0: Structural @mention pre-processor (language-agnostic, always runs)
			// This is evaluated BEFORE IdentifyIntent and the resolver, regardless of specialistID.
			if mentionMatch, remainingTask := o.resolveMention(trimmedText); mentionMatch.Confidence >= 1.0 {
				o.ChatRepo.SaveMessage(&types.Message{
					ID: intentID, ConversationID: conversationID,
					Role: "user", Content: text, Timestamp: time.Now(),
				})

				if mentionMatch.Type == types.IntentTypeReturnControl {
					o.ChatRepo.SaveMessage(&types.Message{
						ID: uuid.New().String(), ConversationID: conversationID,
						Role: "system", Content: "CONTROL RETURNED. The specialist sub-agent has exited the conversation. SYSTEM DIRECTIVE: YOU ARE NOW 'VRAXTER' (The primary Orchestrator). STOP ACTING LIKE THE SPECIALIST. Abandon all sub-agent personas. You MUST now answer the user's latest query using your full capabilities as Vraxter.",
						Timestamp: time.Now(),
					})
					// Signal TUI to clear specialist banner and notify user
					if !sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeSpecialistResult, Content: "||"}) {
						return
					}

					if remainingTask != "" {
						// Re-route the remaining text through the supervisor pipeline
						subStream, err := o.ProcessRawIntent(ctx, conversationID, remainingTask, "", overrideModelID, sourceZone, sourceUser)
						if err == nil {
							for e := range subStream {
								if e.Type != llm.EventTypeDone {
									if !sendEvent(ctx, out, e) {
										return
									}
								}
							}
						}
					}
					return
				}

				// @SpecialistName delegation
				o.ExecEngine.RunFastPath(ctx, out, mentionMatch, conversationID)
				return
			}

			// FAST PATH 1: Exact Command Match
			explicitMatch, intentErr := o.IdentifyIntent(ctx, trimmedText)
			if intentErr != nil && strings.HasPrefix(trimmedText, types.CommandExecutionPrefix) {
				finalStatus = "denied"
				o.recordAudit(ctx, conversationID, actor, audit.EventPolicyEvaluated, map[string]interface{}{
					"check":    "host_exec_syntax",
					"decision": "DENY",
					"reason":   intentErr.Error(),
				})
				sendEvent(ctx, out, llm.StreamEvent{
					Type:    llm.EventTypeToken,
					Content: fmt.Sprintf("\n❌ Host command denied: %v", intentErr),
				})
				return
			}
			if explicitMatch.Confidence == 1.0 {
				o.ChatRepo.SaveMessage(&types.Message{
					ID: intentID, ConversationID: conversationID,
					Role: "user", Content: text, Timestamp: time.Now(),
				})
				o.ExecEngine.RunFastPath(ctx, out, explicitMatch, conversationID)
				return
			}

			// FAST PATH 2: Skill-First Execution (Intent Resolver)
			if specialistID == "" {
				match := o.Resolver.Resolve(ctx, text)
				if match.Confidence >= 0.8 {
					o.ChatRepo.SaveMessage(&types.Message{
						ID: intentID, ConversationID: conversationID,
						Role: "user", Content: text, Timestamp: time.Now(),
					})
					o.ExecEngine.RunFastPath(ctx, out, match, conversationID)
					return
				}
			}

			// Background Index (Zero block)
			go func() {
				if embCfg, embProvider, err := o.StreamCoord.Router.GetEmbeddingProvider(); err == nil {
					embedCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					vectors, embedErr := embProvider.Embed(embedCtx, embCfg.Model, []string{text})
					if embedErr == nil && len(vectors) > 0 {
						_ = o.MemoryRepo.SaveEmbedding(conversationID, text, vectors[0])
					}
				}
			}()
		}

		if err := o.refreshCache(); err != nil {
			slog.Warn("Metadata cache refresh encountered issues", "error", err)
		}

		// Copy cached context snapshots under a short read lock — NEVER hold o.mu across gatherPromptContext.
		o.mu.RLock()
		modelsCtxStr := o.cachedModelsCtx
		specsCtxStr := o.cachedSpecsCtx
		o.mu.RUnlock()

		// 2. Load context in parallel (RAG memory & User Profile)
		userProfilePool, memoryContextPool, memoryCount := o.gatherPromptContext(ctx, trimmedText, sourceUser)
		sourcesLoaded := []string{"models_catalog", "specialists_catalog"}
		if userProfilePool != "" {
			sourcesLoaded = append(sourcesLoaded, "user_profile")
		}
		if memoryCount > 0 {
			sourcesLoaded = append(sourcesLoaded, "rag_memories")
		}
		o.recordAudit(ctx, conversationID, actor, audit.EventContextAssembled, map[string]interface{}{
			"sources":          sourcesLoaded,
			"memory_count":     memoryCount,
			"has_user_profile": userProfilePool != "",
			"isolation_mode":   isoMode,
		})

		// If no model override is provided, but we have a targeted specialist, check if it has a fixed model
		if overrideModelID == "" && specialistID != "" {
			if spec, err := o.SpecialistRepo.GetSpecialist(specialistID); err == nil && spec != nil {
				if spec.ModelID != "" {
					overrideModelID = spec.ModelID
				}
			}
		}

		// LLM EXECUTION: Resolve Model (falls back to use-case intent parsing if no override)
		modelCfg, err := o.resolveExecutionModel(ctx, trimmedText, conversationID, overrideModelID, out)
		if err != nil {
			finalStatus = "error"
			sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeError, Err: err})
			return
		}
		if modelCfg.Alias != "" {
			resolvedModelID = modelCfg.Alias
		} else {
			resolvedModelID = modelCfg.Model
		}

		userMsgContent := text
		if isControlContinuation {
			userMsgContent = trimmedText
		}
		o.ChatRepo.SaveMessage(&types.Message{
			ID: intentID, ConversationID: conversationID,
			Role: "user", Content: userMsgContent, Timestamp: time.Now(),
		})

		maxRetries := 5
		for iteration := 0; iteration < maxRetries; iteration++ {
			if ctx.Err() != nil {
				break
			}

			history := o.ContextMgr.PruneHistory(conversationID, modelCfg.ContextWindow)
			toolsContext := o.buildToolsContext(specialistID)
			systemPrompt, enf := o.buildSystemPrompt(specialistID, specsCtxStr, modelsCtxStr, toolsContext, userProfilePool, memoryContextPool, &modelCfg, sourceZone)

			msgs := []llm.Message{{Role: "system", Content: systemPrompt}}
			for idx, h := range history {
				content := h.Content
				if idx == len(history)-1 && h.Role == "user" {
					content += enf
				}
				msgs = append(msgs, llm.Message{Role: h.Role, Content: content})
			}

			o.mu.RLock()
			temp := o.DefaultTemperature
			seed := o.DefaultSeed
			maxTokens := o.DefaultMaxTokens
			o.mu.RUnlock()
			if temp == nil {
				temp = llm.Float64Ptr(0.0)
			}
			nativeTools := o.buildNativeToolDefinitions(specialistID)

			req := llm.CompletionRequest{
				Model:       modelCfg.Model,
				Messages:    msgs,
				Temperature: temp,
				Seed:        seed,
				MaxTokens:   maxTokens,
				Tools:       nativeTools,
			}
			queue := make(chan types.Event, 100)
			go func() {
				_ = o.StreamCoord.Run(ctx, out, queue, req, markerChat, markerTool, markerCode)
				close(queue)
			}()

			feedback := o.ExecEngine.ExecutePipeline(ctx, queue, out, conversationID)
			if feedback == "" || feedback == "!YIELD_FOR_APPROVAL" {
				break
			}

			if iteration < maxRetries-1 {
				if strings.Contains(feedback, "CONTROL RETURNED") {
					specialistID = ""
				}
				o.ChatRepo.SaveMessage(&types.Message{
					ID: uuid.New().String(), ConversationID: conversationID,
					Role: "user", Content: feedback, Timestamp: time.Now(),
				})
			}
		}

		sendEvent(ctx, out, llm.StreamEvent{Type: llm.EventTypeDone})
	}()

	return out, nil
}

// SetInferenceDefaults configures the default deterministic LLM inference parameters
// (Temperature, Seed, MaxTokens) injected into CompletionRequest.
func (o *Orchestrator) SetInferenceDefaults(temp *float64, seed *int64, maxTokens int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if temp != nil {
		v := *temp
		o.DefaultTemperature = &v
	}
	if seed != nil {
		s := *seed
		o.DefaultSeed = &s
	}
	if maxTokens > 0 {
		o.DefaultMaxTokens = maxTokens
	}
}

// SetAllowRuntimeCodegen toggles whether LLM-initiated runtime WASM code generation
// (vraxter-coder / vraxter-coder-rust) is authorized by policy.
func (o *Orchestrator) SetAllowRuntimeCodegen(allow bool) {
	if o != nil && o.ExecEngine != nil {
		o.ExecEngine.AllowRuntimeCodegen = allow
	}
}

// buildNativeToolDefinitions assembles structured native ToolDefinitions for the active
// authorized built-in tools and installed WASM skills so providers use native function calling.
func (o *Orchestrator) buildNativeToolDefinitions(specialistID string) []llm.ToolDefinition {
	defaultObjSchema := json.RawMessage(`{"type":"object","properties":{}}`)
	seen := make(map[string]bool)
	var tools []llm.ToolDefinition

	addTool := func(name, desc string, schema json.RawMessage) {
		if seen[name] {
			return
		}
		seen[name] = true
		if len(schema) == 0 || !json.Valid(schema) {
			schema = defaultObjSchema
		}
		tools = append(tools, llm.ToolDefinition{
			Name:             name,
			Description:      desc,
			ParametersSchema: schema,
		})
	}

	if o.ExecEngine == nil || o.ExecEngine.AllowRuntimeCodegen {
		addTool("vraxter-coder", "Compile and install a new sandboxed WASM skill.", json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"description":{"type":"string"},"spec":{"type":"string"},"language":{"type":"string"}},"required":["name","description"]}`))
	}

	if specialistID == "" {
		addTool("vraxter-create-specialist", "Register a domain specialist sub-agent.", json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"expertise":{"type":"string"},"model_id":{"type":"string"},"system_prompt":{"type":"string"}},"required":["name","expertise"]}`))
		addTool("vraxter-delete-specialist", "Delete an existing specialist sub-agent.", json.RawMessage(`{"type":"object","properties":{"identifier":{"type":"string"}},"required":["identifier"]}`))
		addTool("vraxter-delegate", "Delegate a task to a specialist sub-agent.", json.RawMessage(`{"type":"object","properties":{"specialist_id":{"type":"string"},"task":{"type":"string"}},"required":["specialist_id","task"]}`))
	} else {
		addTool("vraxter-return-control", "Return execution control to the primary orchestrator.", defaultObjSchema)
	}

	addTool("vraxter-activate-model", "Lock a specific LLM model for the current session.", json.RawMessage(`{"type":"object","properties":{"model_id":{"type":"string"}},"required":["model_id"]}`))
	addTool("vraxter-restore-model", "Restore automatic model routing.", defaultObjSchema)
	addTool("vraxter-read-file", "Read a file inside the confined workspace.", json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`))
	addTool("vraxter-list-dir", "List files inside the confined workspace directory.", json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`))
	addTool("vraxter-patch-code", "Apply a HITL-approved search-and-replace patch to a workspace file.", json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"search":{"type":"string"},"replace":{"type":"string"}},"required":["path","search","replace"]}`))

	if o.ExecEngine != nil && o.ExecEngine.Registry != nil {
		for _, sl := range o.ExecEngine.Registry.GetAll() {
			if seen[sl.ID] {
				continue
			}
			var schema json.RawMessage
			if sl.ManifestV1 != nil && sl.ManifestV1.IO != nil && len(sl.ManifestV1.IO.Input.Schema) > 0 && json.Valid(sl.ManifestV1.IO.Input.Schema) {
				trimmed := strings.TrimSpace(string(sl.ManifestV1.IO.Input.Schema))
				if strings.HasPrefix(trimmed, "{") {
					schema = json.RawMessage(trimmed)
				}
			}
			if len(schema) == 0 && strings.TrimSpace(sl.ParamsSchema) != "" {
				trimmed := strings.TrimSpace(sl.ParamsSchema)
				if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
					schema = json.RawMessage(trimmed)
				}
			}
			addTool(sl.ID, sl.Description, schema)
		}
	}

	return tools
}

// buildToolsContext assembles the tool listing string injected into the system prompt.
func (o *Orchestrator) buildToolsContext(specialistID string) string {
	allSkills := o.ExecEngine.Registry.GetAll()
	var officialSkills, communitySkills []string

	// Use a map to track official tools and prevent description "poisoning" from legacy DB entries
	addedOfficial := make(map[string]bool)

	addOfficial := func(id, desc string) {
		officialSkills = append(officialSkills, fmt.Sprintf("- ID: %s [OFFICIAL] | Description: %s", id, desc))
		addedOfficial[id] = true
	}

	if o.ExecEngine == nil || o.ExecEngine.AllowRuntimeCodegen {
		addOfficial("vraxter-coder", "CREATE NEW SKILLS. Use this when the user needs to create a new skill or executable plugin. Params: name (string, required), description (string, required), spec (string, required: natural language description of what the skill should do, its inputs and outputs), language (string, optional: 'go' or 'rust'), run_params (object, optional: parameters to immediately execute the skill after creation). Vraxter will auto-generate and compile the code to WASM.")
	} else {
		addedOfficial["vraxter-coder"] = true
		addedOfficial["vraxter-coder-rust"] = true
	}

	if specialistID == "" {
		addOfficial("vraxter-create-specialist", "CREATE NEW SPECIALISTS. Use this when the user asks to create or register a new domain specialist sub-agent. Params: name (string, required: unique identifier e.g. 'Security_Auditor'), expertise (string, required: detailed domain skills and responsibilities), model_id (string, optional: specific LLM model ID), system_prompt (string, optional: customized behavioral rules). Example: {\"skill_id\": \"vraxter-create-specialist\", \"params\": {\"name\": \"Security_Auditor\", \"expertise\": \"Vulnerability assessments, security audits, and system hardening\", \"system_prompt\": \"Act as a senior cybersecurity analyst...\"}}")
		addOfficial("vraxter-delete-specialist", "Delete a specialist sub-agent. First parameter must be 'identifier' spanning either ID or Name.")
		addOfficial("vraxter-delegate", "Hand over user query to a specialist sub-agent. Params: specialist_id, task.")
	} else {
		addOfficial("vraxter-return-control", "Hand control back to main supervisor Vraxter if query is out of your domain.")
	}

	addOfficial("vraxter-activate-model", "Manually switch and lock a specific model for the current session. Use this ONLY if the user explicitly requests to change models. Params: model_id.")
	addOfficial("vraxter-restore-model", "Unlock model routing and restore automatic selection based on intent.")

	addOfficial("vraxter-read-file", "Read the contents of a local file. Use this to read files from the workspace. Params: path.")
	addOfficial("vraxter-list-dir", "List the contents of a local directory. Use this to explore the workspace. Params: path.")

	for _, sl := range allSkills {
		if addedOfficial[sl.ID] {
			continue // Skip duplicate/legacy descriptions from DB
		}

		if sl.IsOfficial {
			officialSkills = append(officialSkills, fmt.Sprintf("- ID: %s [OFFICIAL] | Description: %s", sl.ID, sl.Description))
		} else {
			paramsInfo := ""
			if sl.ParamsSchema != "" {
				paramsInfo = fmt.Sprintf(" | Schema: %s", sl.ParamsSchema)
			} else if sl.ParamRegex != "" {
				paramsInfo = " | [Regex Fast-Path]"
			}
			communitySkills = append(communitySkills, fmt.Sprintf("- ID: %s | Description: %s%s", sl.ID, sl.Description, paramsInfo))
		}
	}

	ctx := strings.Join(officialSkills, "\n")
	if len(communitySkills) > 0 {
		ctx += "\n\nCOMMUNITY TOOLS:\n" + strings.Join(communitySkills, "\n")
	}
	if len(allSkills) == 0 && len(officialSkills) == 1 {
		ctx = "Only 'vraxter-coder' is available."
	}
	return ctx
}

func (o *Orchestrator) buildSystemPrompt(
	specialistID, specsCtxStr, modelsCtxStr, toolsContext, userProfile, memoryContext string,
	modelCfg *types.ModelConfig, sourceZone string,
) (string, string) {
	enforcementSuffix := fmt.Sprintf("\n\n[PROMPT LOCK: You MUST strictly mirror the user language in EVERY response.]\n[PROTOCOL LOCK: IF a functional tool is needed, use: %s, %s, %s. If the task is EDUCATIONAL (snippets/explanations), DO NOT use tools. Use PLAIN Markdown chat.]\n[CRITICAL: DO NOT attempt to write code or create skills to perform 'model activation' or 'switching'. Use the built-in 'vraxter-activate-model' tool instead.]\n[TOOL USAGE: To invoke a tool, YOU MUST OUTPUT STRICTLY VALID JSON. The JSON must contain the 'skill_id' of the tool you are invoking, and a 'params' object. Example: %s {\"skill_id\": \"vraxter-coder\", \"params\": {\"name\": \"data_formatter\", \"description\": \"Format dataset\", \"spec\": \"Transform inputs into standardized schema\"}}]\n[CAPABILITY LOCK: You ARE running inside the Vraxter autonomous engine. Vraxter will natively compile and execute your code. YOU MUST NOT refuse to write code. NEVER say 'I cannot execute code'.]\n[RETRY LOCK: If a tool execution fails and returns an error, you MUST correct the payload and invoke the tool again. NEVER give up and dump raw code as markdown.]",
		markerChat, markerTool, markerCode, markerTool)

	gitCtx := utils.CaptureGitContext("")

	if specialistID != "" {
		spec, err := o.SpecialistRepo.GetSpecialist(specialistID)
		if err == nil {
			if spec.ModelID != "" {
				modelCfg.Model = spec.ModelID
			}

			out, err := prompts.RenderSpecialist(prompts.SpecialistParams{
				SpecialistName:      spec.Name,
				SpecialistExpertise: spec.Expertise,
				SpecialistPrompt:    spec.SystemPrompt,
				AvailableTools:      toolsContext,
				EnforcementSuffix:   enforcementSuffix,
				GitContext:          gitCtx,
				UserProfile:         userProfile,
				SemanticMemory:      memoryContext,
				MarkerChat:          markerChat,
				MarkerTool:          markerTool,
				MarkerCode:          markerCode,
				SourceZone:          sourceZone,
			})
			if err == nil {
				return out, enforcementSuffix
			}
			slog.Error("failed to render specialist prompt", "err", err)
		}
	}

	out, err := prompts.RenderBaseSystem(prompts.BaseSystemParams{
		ExistingSpecialists: specsCtxStr,
		AvailableModels:     modelsCtxStr,
		AvailableTools:      toolsContext,
		MarkerChat:          markerChat,
		MarkerTool:          markerTool,
		MarkerCode:          markerCode,
		GitContext:          gitCtx,
		UserProfile:         userProfile,
		SemanticMemory:      memoryContext,
		SourceZone:          sourceZone,
	})
	if err == nil {
		return out, enforcementSuffix
	}
	slog.Error("failed to render base system prompt", "err", err)
	return "Error rendering system prompt fallback.", enforcementSuffix
}

// summarizeConversationBackground computes a text summary of the oldest `n` messages
// and replaces it in the conversation record securely in an async fashion.
func (o *Orchestrator) summarizeConversationBackground(conversationID string, history []types.Message) {
	if len(history) < 10 {
		return
	}
	// We extract the oldest 10 messages for summarization.
	var batch strings.Builder
	for i := 0; i < 10; i++ {
		batch.WriteString(fmt.Sprintf("%s: %s\n", history[i].Role, history[i].Content))
	}

	orderedProviders := o.StreamCoord.Router.GetOrderedProviders()
	if len(orderedProviders) == 0 {
		return
	}

	summaryPrompt := "Summarize the following chat history briefly so we don't lose the overarching context or the active intent of the user. Keep it to a solid paragraph max. \n\n" + batch.String()

	req := llm.CompletionRequest{
		Model: orderedProviders[0].Config.Model,
		Messages: []llm.Message{
			{Role: "user", Content: summaryPrompt},
		},
	}

	resp, err := orderedProviders[0].Provider.Generate(context.Background(), req)
	if err == nil && resp.Content != "" {
		if o.Verbose {
			slog.Info("Successfully summarized history block", "new_summary", resp.Content)
		}
		_ = o.ChatRepo.UpdateConversationSummary(conversationID, resp.Content)
		_ = o.ChatRepo.TruncateOldMessages(conversationID, 5) // Keep only the top 5 newest!
	}
}

// DelegateSwarmTask spawns an isolated execution thread for a sub-agent to fulfill a specific atomic sub-task.
// It intercepts the stream response and returns the finalized String output completely back to the master agent executing it.
func (o *Orchestrator) DelegateSwarmTask(ctx context.Context, out chan<- llm.StreamEvent, specialistID string, task string, threadID string) string {
	slog.Info("Spawning Swarm Sub-Agent Thread", "specialist", specialistID, "thread_id", threadID, "task", task)

	// 1. Focus Shift: Tell the TUI to prepare the specialist banner
	specName := specialistID
	if spec, err := o.SpecialistRepo.GetSpecialist(specialistID); err == nil && spec != nil {
		specName = spec.Name
	}
	if !sendEvent(ctx, out, llm.StreamEvent{
		Type:    llm.EventTypeSpecialistResult,
		Content: fmt.Sprintf("%s|%s|", specialistID, specName), // Content empty for focus shift
	}) {
		return ""
	}

	// In a swarm architecture, the sub-agent needs a unique session id so its history doesn't inherently corrupt the master thread, but we can link them conceptually.
	subSessionID := fmt.Sprintf("swarm_%s_%s", specialistID, uuid.New().String())

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Intercept the stream of the newly spawned orchestration loop
	outStream, err := o.ProcessRawIntent(subCtx, subSessionID, task, specialistID, "", "", "")
	if err != nil {
		return fmt.Sprintf("SUB-AGENT CRASHED: %v", err)
	}

	var accumulatedResp strings.Builder
	for e := range outStream {
		switch e.Type {
		case llm.EventTypeToken:
			// ◈ NATIVE PIPE: Pass token directly to parent stream without blocking on canceled context
			if !sendEvent(ctx, out, e) {
				cancel()
				return strings.TrimSpace(accumulatedResp.String())
			}
			accumulatedResp.WriteString(e.Content)
		case llm.EventTypeError:
			return fmt.Sprintf("SUB-AGENT ERRORED EARLY: %s. Output before crash: %s", e.Content, accumulatedResp.String())
		}
	}

	// FALLBACK: If the stream was tool-only (no tokens), pull the latest result from the sub-session history.
	finalResp := strings.TrimSpace(accumulatedResp.String())

	if finalResp == "" {
		messages, err := o.ChatRepo.GetMessagesByConversation(subSessionID, 5)
		if err == nil && len(messages) > 0 {
			// Find the last assistant message
			for i := len(messages) - 1; i >= 0; i-- {
				if messages[i].Role == "assistant" && messages[i].Content != "" {
					return messages[i].Content
				}
			}
		}
		return "ERROR: Sub-agent failed to return an answer."
	}

	return finalResp
}

// executeSubTask is an internal helper that runs a plan phase by recursively calling ProcessRawIntent
// and piping its stream output back to the primary orchestration channel.
func (o *Orchestrator) executeSubTask(ctx context.Context, query, specialistID string, out chan<- llm.StreamEvent) error {
	subSessionID := fmt.Sprintf("phase_%s", uuid.New().String())

	// We call ProcessRawIntent recursively for the sub-task
	subStream, err := o.ProcessRawIntent(ctx, subSessionID, query, specialistID, "", "", "")
	if err != nil {
		return err
	}
	for event := range subStream {
		if event.Type != llm.EventTypeDone {
			select {
			case out <- event:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

// SetSessionOverride locks a specific model for all future requests in this session.
func (o *Orchestrator) SetSessionOverride(sessionID, modelID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if modelID == "" {
		delete(o.sessionOverrides, sessionID)
		return nil
	}

	// Validate if the model ID or alias exists
	_, _, err := o.StreamCoord.Router.GetProviderByID(modelID)
	if err != nil {
		return fmt.Errorf("invalid model ID or alias: '%s'", modelID)
	}

	o.sessionOverrides[sessionID] = modelID
	return nil
}

// ClearSessionOverride restores automatic routing for the session.
func (o *Orchestrator) ClearSessionOverride(sessionID string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.sessionOverrides, sessionID)
}
