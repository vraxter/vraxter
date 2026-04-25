package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/shlex"

	"github.com/google/uuid"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/prompts"
	"github.com/patagonicrune/vraxter/internal/security"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/internal/utils"
	"github.com/patagonicrune/vraxter/pkg/types"
)

const (
	markerChat = "[VRAX_CHAT]"
	markerTool = "[VRAX_TOOL]"
	markerCode = "[VRAX_CODE]"
)

// Orchestrator is the primary coordinator of Vraxter's request lifecycle.
// It owns the high-level flow: resolve intent → execute fast-path or delegate to LLM.
type Orchestrator struct {
	// Repositories
	ModelsRepo     *db.ModelRepository
	ChatRepo       *db.ChatRepository
	SpecialistRepo *db.SpecialistRepository
	MemoryRepo     *db.MemoryRepository

	// Sub-controllers
	Resolver    *IntentResolver
	ExecEngine  *ExecutionEngine
	StreamCoord *StreamCoordinator
	Planner     *Planner
	UserRepo    *db.UserRepository
	MemorySvc   *services.MemoryService


	// Performance Cache
	mu                sync.RWMutex
	cachedModels      []types.ModelConfig
	cachedSpecialists []types.Specialist
	cachedModelsCtx   string
	cachedSpecsCtx    string
	cacheExpiry       time.Time

	// Session State
	sessionOverrides map[string]string // sessionID -> modelID lock

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
) (*Orchestrator, error) {
	modelsRepo := db.NewModelRepository(store, crypto)
	chatRepo := db.NewChatRepository(store)
	specRepo := db.NewSpecialistRepository(store)
	memoryRepo := db.NewMemoryRepository(store)

	models, err := modelsRepo.GetActiveModels()
	if err != nil {
		return nil, fmt.Errorf("failed fetching models from local DB: %w", err)
	}

	router := llm.NewRouter(models)
	for _, m := range models {
		switch m.Provider {
		case "ollama":
			router.Register(m.ID, llm.NewOllamaAdapter(m.BaseURL))
		case "openai":
			router.Register(m.ID, llm.NewOpenAIAdapter(m.APIKey))
		case "anthropic":
			router.Register(m.ID, llm.NewAnthropicAdapter(m.APIKey))
		case "gemini":
			router.Register(m.ID, llm.NewGeminiAdapter(m.APIKey))
		}
	}

	// Wire use-case routing overrides from routing.yaml
	for useCase, modelID := range routingOverrides {
		router.SetUseCaseOverride(useCase, modelID)
	}

	skillRepo := db.NewSkillRepository(store)
	o := &Orchestrator{
		ModelsRepo:       modelsRepo,
		ChatRepo:         chatRepo,
		SpecialistRepo:   specRepo,
		MemoryRepo:       memoryRepo,
		UserRepo:         db.NewUserRepository(store),
		Resolver:         NewIntentResolver(reg, router, skillRepo, specRepo),
		ExecEngine:       NewExecutionEngine(reg, run, coder, chatRepo, specRepo, verbose),
		StreamCoord:      NewStreamCoordinator(router, verbose),
		Planner:          NewPlanner(chatRepo, router),
		MemorySvc:        services.NewMemoryService(memoryRepo, router),
		Verbose:          verbose,
		sessionOverrides: make(map[string]string),
	}

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

	o.ExecEngine.SwarmDelegate = o.DelegateSwarmTask
	o.ExecEngine.ActivateModelDelegate = o.SetSessionOverride
	o.ExecEngine.RestoreModelDelegate = o.ClearSessionOverride
	o.ExecEngine.ResolveSpecialistDelegate = o.Resolver.FindSpecialistSemantically
	return o, nil
}

// refreshCache keeps in-memory metadata fresh (max 30s staleness).
func (o *Orchestrator) refreshCache() error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if time.Now().Before(o.cacheExpiry) {
		return nil
	}

	models, err := o.ModelsRepo.GetAllModelsPublic()
	if err != nil {
		return err
	}
	specs, err := o.SpecialistRepo.GetAllSpecialists()
	if err != nil {
		return err
	}

	o.cachedModels = models
	o.cachedSpecialists = specs

	// Rebuild model/spec context strings
	var mCtx strings.Builder
	for _, m := range models {
		status := "READY"
		if m.Provider == "ollama" && m.BaseURL == "" {
			status = "MISSING_CONFIG (Base URL Required)"
		} else if (m.Provider == "openai" || m.Provider == "gemini" || m.Provider == "anthropic") && m.APIKey == "" {
			status = "MISSING_CONFIG (API Key Required)"
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
		sCtx.WriteString(fmt.Sprintf("- ID: '%s' | Name: %s | Expertise: %s\n", s.ID, s.Name, s.Expertise))
	}
	o.cachedSpecsCtx = sCtx.String()
	if o.cachedSpecsCtx == "" {
		o.cachedSpecsCtx = "None.\n"
	}

	o.cacheExpiry = time.Now().Add(30 * time.Second)
	return nil
}

func (o *Orchestrator) IdentifyIntent(ctx context.Context, text string) (types.IntentMatch, error) {
	if len(text) == 0 {
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
		args, err := shlex.Split(rawPayload)
		if err != nil || len(args) == 0 {
			return types.IntentMatch{}, fmt.Errorf("failed to parse command: %v", err)
		}

		return types.IntentMatch{
			Type:       types.IntentTypeCommand,
			ID:         args[0],
			Args:       args[1:],
			Confidence: 1.0,
			Params:     map[string]interface{}{},
		}, nil
	}

	return types.IntentMatch{}, nil
}

func (o *Orchestrator) GetActiveModelID(sessionID string) string {
	if sessionID != "" {
		o.mu.RLock()
		if lock, ok := o.sessionOverrides[sessionID]; ok {
			o.mu.RUnlock()
			return lock
		}
		o.mu.RUnlock()
	}

	ordered := o.StreamCoord.Router.GetOrderedProviders()
	if len(ordered) > 0 {
		return ordered[0].Config.Model
	}
	return "unknown"
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

func (o *Orchestrator) gatherPromptContext(ctx context.Context, text string) (string, string) {
	var userProfilePool, memoryContextPool string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		user, err := o.UserRepo.GetDefaultUser(ctx)
		if err == nil && user != nil {
			userProfilePool = fmt.Sprintf("User: %s. expertise: %s.", user.Name, user.Expertise)
		}
	}()
	go func() {
		defer wg.Done()
		ragCtx, cancel := context.WithTimeout(ctx, 8*time.Second) // Strict RAG timeout
		defer cancel()
		memories, _ := o.MemorySvc.SearchMemories(ragCtx, text, 3)
		if len(memories) > 0 {
			var b strings.Builder
			for _, m := range memories {
				b.WriteString("- " + m.Content + "\n")
			}
			memoryContextPool = b.String()
		}
	}()
	wg.Wait()
	return userProfilePool, memoryContextPool
}

func (o *Orchestrator) resolveExecutionModel(text, sessionID, overrideModelID string) (types.ModelConfig, error) {
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
		ordered := o.StreamCoord.Router.GetOrderedProvidersForUseCase(useCase)
		if len(ordered) == 0 {
			return modelCfg, fmt.Errorf("no LLM models configured")
		}
		modelCfg = ordered[0].Config
	}
	return modelCfg, nil
}

func (o *Orchestrator) ProcessRawIntent(ctx context.Context, sessionID, text, specialistID, overrideModelID string) (<-chan llm.StreamEvent, error) {
	out := make(chan llm.StreamEvent)
	intentID := uuid.New().String()

	go func() {
		defer close(out)

		// 1. Initial Status message to confirm responsiveness
		select {
		case out <- llm.StreamEvent{Type: llm.EventTypeStatus, Content: "Analyzing query & gathering context..."}:
		case <-ctx.Done():
			return
		}

		// Resolve conversation early
		conversationID := o.resolveConversationContext(sessionID, specialistID)

		trimmedText := strings.TrimSpace(text)
		
		// FAST PATH 1: Exact Command Match
		explicitMatch, _ := o.IdentifyIntent(ctx, trimmedText)
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
				vectors, embedErr := embProvider.Embed(context.Background(), embCfg.Model, []string{text})
				if embedErr == nil && len(vectors) > 0 {
					_ = o.MemoryRepo.SaveEmbedding(conversationID, text, vectors[0])
				}
			}
		}()

		if err := o.refreshCache(); err != nil {
			slog.Warn("Metadata cache refresh encountered issues", "error", err)
		}

		o.mu.RLock()
		modelsCtxStr := o.cachedModelsCtx
		specsCtxStr := o.cachedSpecsCtx

		// PLANNING & AMENDMENTS
		if strings.HasPrefix(trimmedText, "/plan") {
			o.mu.RUnlock()
			query := strings.TrimPrefix(trimmedText, "/plan")
			plan, err := o.Planner.BuildPlan(ctx, strings.TrimSpace(query), specsCtxStr)
			if err != nil {
				out <- llm.StreamEvent{Type: llm.EventTypeError, Err: err}
				return
			}
			planJSON, _ := json.Marshal(plan)
			out <- llm.StreamEvent{Type: llm.EventTypePlanProposal, Content: string(planJSON)}
			return
		}

		if strings.HasPrefix(trimmedText, "!EXECUTE_PLAN") {
			o.mu.RUnlock()
			planJSON := strings.TrimPrefix(trimmedText, "!EXECUTE_PLAN")
			var plan Plan
			if err := json.Unmarshal([]byte(planJSON), &plan); err == nil {
				for i, phase := range plan.Phases {
					if ctx.Err() != nil { return }
					out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n\n[▶️ Phase %d: %s]\n", i+1, phase.Title)}
					phaseQuery := fmt.Sprintf("TASK: %s\nDESCRIPTION: %s", phase.Title, phase.Description)
					_ = o.executeSubTask(ctx, phaseQuery, phase.Specialist, out)
				}
				out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n\n✅ Plan Complete"}
			}
			return
		}

		// CONTEXT GATHERING
		userProfilePool, memoryContextPool := o.gatherPromptContext(ctx, trimmedText)
		o.mu.RUnlock()

		// LLM EXECUTION: Resolve Model
		modelCfg, err := o.resolveExecutionModel(trimmedText, sessionID, overrideModelID)
		if err != nil {
			out <- llm.StreamEvent{Type: llm.EventTypeError, Err: err}
			return
		}

		o.ChatRepo.SaveMessage(&types.Message{
			ID: intentID, ConversationID: conversationID,
			Role: "user", Content: text, Timestamp: time.Now(),
		})

		maxRetries := 2
		for iteration := 0; iteration < maxRetries; iteration++ {
			if ctx.Err() != nil { break }

			history, _ := o.ChatRepo.GetMessagesByConversation(conversationID, 12)
			toolsContext := o.buildToolsContext(specialistID)
			systemPrompt, enf := o.buildSystemPrompt(specialistID, specsCtxStr, modelsCtxStr, toolsContext, userProfilePool, memoryContextPool, &modelCfg)
			
			msgs := []llm.Message{{Role: "system", Content: systemPrompt}}
			for idx, h := range history {
				content := h.Content
				if idx == len(history)-1 && h.Role == "user" { content += enf }
				msgs = append(msgs, llm.Message{Role: h.Role, Content: content})
			}

			req := llm.CompletionRequest{Model: modelCfg.Model, Messages: msgs}
			queue := make(chan types.Event, 100)
			go func() {
				_ = o.StreamCoord.Run(ctx, out, queue, req, markerChat, markerTool, markerCode)
				close(queue)
			}()

			feedback := o.ExecEngine.ExecutePipeline(ctx, queue, out, conversationID)
			if feedback == "" { break }

			if iteration < maxRetries-1 {
				if strings.Contains(feedback, "CONTROL RETURNED") { specialistID = "" }
				o.ChatRepo.SaveMessage(&types.Message{
					ID: uuid.New().String(), ConversationID: conversationID,
					Role: "system", Content: feedback, Timestamp: time.Now(),
				})
			}
		}

		select {
		case out <- llm.StreamEvent{Type: llm.EventTypeDone}:
		case <-ctx.Done():
		}
	}()

	return out, nil
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

	addOfficial("vraxter-coder", "CREATE NEW TOOLS. Use this when the user needs a tool that doesn't exist. Params: name, description, logic (Go code).")
	
	if specialistID == "" {
		addOfficial("vraxter-create-specialist", "Create a Sub-Agent specialist. Params: name, expertise, model_id (optional).")
		addOfficial("vraxter-delete-specialist", "Delete a specialist sub-agent. First parameter must be 'identifier' spanning either ID or Name.")
		addOfficial("vraxter-delegate", "Hand over user query to a specialist sub-agent. Params: specialist_id, task.")
	} else {
		addOfficial("vraxter-return-control", "Hand control back to main supervisor Vraxter if query is out of your domain.")
	}

	addOfficial("vraxter-activate-model", "Manually switch and lock a specific model for the current session. Use this ONLY if the user explicitly requests to change models. Params: model_id.")
	addOfficial("vraxter-restore-model", "Unlock model routing and restore automatic selection based on intent.")

	for _, sl := range allSkills {
		if addedOfficial[sl.ID] {
			continue // Skip duplicate/legacy descriptions from DB
		}
		
		if sl.IsOfficial {
			officialSkills = append(officialSkills, fmt.Sprintf("- ID: %s [OFFICIAL] | Description: %s", sl.ID, sl.Description))
		} else {
			communitySkills = append(communitySkills, fmt.Sprintf("- ID: %s | Description: %s (Trust Score: %.1f)", sl.ID, sl.Description, sl.Score))
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
	modelCfg *types.ModelConfig,
) (string, string) {
	enforcementSuffix := fmt.Sprintf("\n\n[PROMPT LOCK: You MUST strictly mirror the user language in EVERY response.]\n[PROTOCOL LOCK: IF a functional tool is needed, use: %s, %s, %s. If the task is EDUCATIONAL (snippets/explanations), DO NOT use tools. Use PLAIN Markdown chat.]\n[CRITICAL: DO NOT attempt to write code or create skills to perform 'model activation' or 'switching'. Use the built-in 'vraxter-activate-model' tool instead. Hallucinating execution scripts for configuration changes is strictly Forbidden.]",
		markerChat, markerTool, markerCode)

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
	out <- llm.StreamEvent{
		Type:    llm.EventTypeSpecialistResult,
		Content: fmt.Sprintf("%s|%s|", specialistID, specName), // Content empty for focus shift
	}

	// In a swarm architecture, the sub-agent needs a unique session id so its history doesn't inherently corrupt the master thread, but we can link them conceptually.
	subSessionID := fmt.Sprintf("swarm_%s_%s", specialistID, uuid.New().String())

	subCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Intercept the stream of the newly spawned orchestration loop
	outStream, err := o.ProcessRawIntent(subCtx, subSessionID, task, specialistID, "")
	if err != nil {
		return fmt.Sprintf("SUB-AGENT CRASHED: %v", err)
	}

	var accumulatedResp strings.Builder
	for e := range outStream {
		switch e.Type {
		case llm.EventTypeToken:
			// ◈ NATIVE PIPE: Pass token directly to parent stream
			out <- e
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
	subStream, err := o.ProcessRawIntent(ctx, subSessionID, query, specialistID, "")
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

