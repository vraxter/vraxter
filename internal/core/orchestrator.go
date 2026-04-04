package core

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/patagonicrune/vraxter/internal/db"
	"github.com/patagonicrune/vraxter/internal/llm"
	"github.com/patagonicrune/vraxter/internal/security"
	"github.com/patagonicrune/vraxter/internal/services"
	"github.com/patagonicrune/vraxter/internal/skills"
	"github.com/patagonicrune/vraxter/pkg/types"
	"github.com/patagonicrune/vraxter/pkg/vraxerror"
)

const (
	markerChat = "<<<VRAX_CHAT>>>"
	markerTool = "<<<VRAX_TOOL>>>"
	markerCode = "<<<VRAX_CODE>>>"
)

// Orchestrator is the primary coordinator of Vraxter's request lifecycle.
// It owns the high-level flow: resolve intent → execute fast-path or delegate to LLM.
type Orchestrator struct {
	// Repositories
	ModelsRepo     *db.ModelRepository
	ChatRepo       *db.ChatRepository
	SpecialistRepo *db.SpecialistRepository

	// Sub-controllers
	Resolver    *IntentResolver
	ExecEngine  *ExecutionEngine
	StreamCoord *StreamCoordinator

	// Performance Cache
	mu                 sync.RWMutex
	cachedModels       []types.ModelConfig
	cachedSpecialists  []types.Specialist
	cachedModelsCtx    string
	cachedSpecsCtx     string
	cacheExpiry        time.Time

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
) (*Orchestrator, error) {
	modelsRepo := db.NewModelRepository(store, crypto)
	chatRepo := db.NewChatRepository(store)
	specRepo := db.NewSpecialistRepository(store)

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

	return &Orchestrator{
		ModelsRepo:     modelsRepo,
		ChatRepo:       chatRepo,
		SpecialistRepo: specRepo,
		Resolver:       NewIntentResolver(reg, router),
		ExecEngine:     NewExecutionEngine(reg, run, coder, chatRepo, specRepo, verbose),
		StreamCoord:    NewStreamCoordinator(router, verbose),
		Verbose:        verbose,
	}, nil
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

// ProcessRawIntent is the single entry point for all user queries.
// It routes to the fast-path or the LLM path depending on intent confidence.
func (o *Orchestrator) ProcessRawIntent(ctx context.Context, sessionID, text, specialistID string) (<-chan llm.StreamEvent, error) {
	out := make(chan llm.StreamEvent)

	intentID := uuid.New().String()

	// Resolve conversation early (needed by both fast-path and LLM path)
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
		// Lazily create session-based topic if not exists
		c, err := o.ChatRepo.FindConversation(conversationID)
		if err != nil || c == nil {
			o.ChatRepo.CreateConversation(&types.Conversation{
				ID:    conversationID,
				Title: "Project Context Topic",
			})
		}
	}

	trimmedText := strings.TrimSpace(text)

	// ⚡ Explicit Path: Direct Override (!)
	if strings.HasPrefix(trimmedText, "!") {
		parts := strings.SplitN(trimmedText[1:], " ", 2)
		skillID := parts[0]
		paramStr := ""
		if len(parts) > 1 {
			paramStr = parts[1]
		}
		
		match := types.IntentMatch{
			SkillID:    skillID,
			Confidence: 1.0,
			Params:     map[string]interface{}{"query": paramStr},
		}

		if o.Verbose {
			slog.Info("Explicit override trigger detected", "skill_id", skillID)
		}
		go func() {
			defer close(out)
			o.ChatRepo.SaveMessage(&types.Message{
				ID: intentID, ConversationID: conversationID,
				Role: "user", Content: text, Timestamp: time.Now(),
			})
			o.ExecEngine.RunFastPath(ctx, out, match, conversationID)
			out <- llm.StreamEvent{Type: llm.EventTypeDone}
		}()
		return out, nil
	}

	// ⚡ Fast Path: Skill-First Execution (Intent Resolver)
	if specialistID == "" {
		match := o.Resolver.Resolve(ctx, text)
		if match.Confidence >= 0.8 {
			if o.Verbose {
				slog.Info("Fast-path intent matched seamlessly", "confidence", match.Confidence, "skill_id", match.SkillID)
			}
			go func() {
				defer close(out)
				o.ChatRepo.SaveMessage(&types.Message{
					ID: intentID, ConversationID: conversationID,
					Role: "user", Content: text, Timestamp: time.Now(),
				})
				o.ExecEngine.RunFastPath(ctx, out, match, conversationID)
				out <- llm.StreamEvent{Type: llm.EventTypeDone}
			}()
			return out, nil
		}
		if o.Verbose {
			slog.Info("Low intent confidence, falling back to LLM", "confidence", match.Confidence)
		}
	}

	// 🧠 LLM Path: build context and stream
	if err := o.refreshCache(); err != nil {
		slog.Warn("Metadata cache refresh encountered issues", "error", err)
	}

	o.mu.RLock()
	allModels := o.cachedModels
	allSpecialists := o.cachedSpecialists
	modelsCtxStr := o.cachedModelsCtx
	specsCtxStr := o.cachedSpecsCtx
	o.mu.RUnlock()

	orderedProviders := o.StreamCoord.Router.GetOrderedProviders()
	if len(orderedProviders) == 0 {
		return nil, vraxerror.New(vraxerror.ErrTypeInternal, "no configured model providers available for LLM inference", false, nil)
	}
	modelCfg := orderedProviders[0].Config

	toolsContext := o.buildToolsContext(specialistID)
	systemPrompt := o.buildSystemPrompt(specialistID, specsCtxStr, modelsCtxStr, toolsContext, allSpecialists, allModels, &modelCfg)

	// Inject background summary if present
	convo, _ := o.ChatRepo.FindConversation(conversationID)
	if convo != nil && convo.Summary != "" {
		systemPrompt += "\n\n[CONTEXT SUMMARY FOR THIS TOPIC]\n" + convo.Summary
	}

	enforcementSuffix := "\n\n[CRITICAL SYSTEM DIRECTIVE: IF your answer REQUIRES YOU to GENERATE A TOOL, you MUST use the EXACT 3 blocks (<<<VRAX_CHAT>>>, <<<VRAX_TOOL>>>, <<<VRAX_CODE>>>) as instructed. NO MARKDOWN. The VRAX_TOOL block MUST be valid JSON. BUT IF you can answer directly without tools, DO NOT USE BLOCKS, JUST CHAT!]"

	msgs := []llm.Message{{Role: "system", Content: systemPrompt}}
	history, _ := o.ChatRepo.GetMessagesByConversation(conversationID, 15) // Keep rolling short to 15
	
	// Trigger background context summarization if we hit the boundary point (15 messages)
	// We check for exact 15 to perform the summary precisely before next queries blow up
	if len(history) == 15 {
		if o.Verbose {
			slog.Info("Conversation hit memory limit. Truncating and summarizing in background...", "topic", conversationID)
		}
		// Fork safe copy
		historyCopy := make([]types.Message, len(history))
		copy(historyCopy, history)
		go o.summarizeConversationBackground(conversationID, historyCopy)
	}

	for _, h := range history {
		msgs = append(msgs, llm.Message{Role: h.Role, Content: h.Content})
	}
	msgs = append(msgs, llm.Message{Role: "user", Content: text + enforcementSuffix})

	o.ChatRepo.SaveMessage(&types.Message{
		ID: intentID, ConversationID: conversationID,
		Role: "user", Content: text, Timestamp: time.Now(),
	})

	req := llm.CompletionRequest{Model: modelCfg.Model, Messages: msgs}

	go func() {
		defer close(out)
		queue := make(chan types.Event, 100)

		// 1. Run Parser autonomously (Producer)
		go func() {
			_ = o.StreamCoord.Run(ctx, out, queue, req, markerChat, markerTool, markerCode)
			close(queue)
		}()

		// 2. Consume Event Queue autonomously (Consumer)
		o.ExecEngine.ExecutePipeline(ctx, queue, out, conversationID)
	}()

	return out, nil
}

// buildToolsContext assembles the tool listing string injected into the system prompt.
func (o *Orchestrator) buildToolsContext(specialistID string) string {
	allSkills := o.ExecEngine.Registry.GetAll()
	var officialSkills, communitySkills []string

	officialSkills = append(officialSkills, "- ID: vraxter-coder [OFFICIAL] | Description: CREATE NEW TOOLS. Use this when the user needs a tool that doesn't exist. Params: name, description, logic (Go code).")
	if specialistID == "" {
		officialSkills = append(officialSkills, "- ID: vraxter-create-specialist [OFFICIAL] | Description: Create a Sub-Agent specialist. Params: name, expertise, model_id (optional).")
		officialSkills = append(officialSkills, "- ID: vraxter-delegate [OFFICIAL] | Description: Hand over user query to a specialist. Params: specialist_id.")
	} else {
		officialSkills = append(officialSkills, "- ID: vraxter-return-control [OFFICIAL] | Description: Hand control back to main supervisor Vraxter if query is out of your domain.")
	}
	for _, sl := range allSkills {
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

// buildSystemPrompt composes the full system prompt for the LLM call.
func (o *Orchestrator) buildSystemPrompt(
	specialistID, specsCtxStr, modelsCtxStr, toolsContext string,
	_ []types.Specialist,
	_ []types.ModelConfig,
	modelCfg *types.ModelConfig,
) string {
	enforcementSuffix := "\n\n[CRITICAL SYSTEM DIRECTIVE: IF your answer REQUIRES YOU to GENERATE A TOOL, you MUST use the EXACT 3 blocks (<<<VRAX_CHAT>>>, <<<VRAX_TOOL>>>, <<<VRAX_CODE>>>) as instructed. NO MARKDOWN. The VRAX_TOOL block MUST be valid JSON. BUT IF you can answer directly without tools, DO NOT USE BLOCKS, JUST CHAT!]"

	base := fmt.Sprintf(`### VRAXTER PROTOCOL: THE AGNOSTIC MULTI-PART DESIGNER ###
You are Vraxter, an autonomous agent engine. Your goal is to help the user by using tools, creating tools, or answering directly using your base knowledge.
CRITICAL RULES for Specialists:
A) DELEGATION: If the user's query heavily overlaps with an EXISTING SPECIALIST's expertise, you MUST invoke 'vraxter-delegate' IMMEDIATELY. Do not try to answer natively.
B) CREATION: NEVER use 'vraxter-create-specialist' on the first question of a new topic. ONLY create a specialist if explicitly requested.
C) GENERAL: If you can answer a generic query directly, DO NOT CREATE OR USE TOOLS. Just reply naturally via chat.

EXISTING SPECIALISTS:
%s

AVAILABLE MODELS:
%s

AVAILABLE TOOLS:
%s

SKILL CREATION PROTOCOL (vraxter-coder / vraxter-coder-rust):
ONLY when a tool is absolutely required, respond EXACTLY in 3 DISTINCT blocks WITHOUT markdown:
1. %s: A friendly confirmation.
2. %s: {"skill_id": "vraxter-coder", "params": {"name": "NAME", "description": "DESC"}}
3. %s: The COMPLETE source file (main.go or main.rs).
CRITICAL: Go standard lib only. NO PYTHON. NO MARKDOWN. NO BACKTICKS inside the code block.

GO BOILERPLATE:
package main
import ("encoding/json"; "os"; "fmt")
func main() {
	var req struct { Params map[string]interface{} "json:\"params\""; ID string "json:\"id\"" }
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil { return }
	if req.Params["_vraxter_dry_run"] == true {
		fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":\"%%s\",\"result\":{\"status\":\"completed\",\"output\":\"dry-run success\"}}", req.ID)
		return
	}
	output := "Result"
	fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":\"%%s\",\"result\":{\"status\":\"completed\",\"output\":\"%%s\"}}", req.ID, output)
}
TARGET: GOOS=wasip1, GOARCH=wasm`, specsCtxStr, modelsCtxStr, toolsContext, markerChat, markerTool, markerCode)

	if specialistID != "" {
		spec, err := o.SpecialistRepo.GetSpecialist(specialistID)
		if err == nil {
			if spec.ModelID != "" {
				modelCfg.Model = spec.ModelID
			}
			return fmt.Sprintf(`### VRAXTER SPECIALIST PROTOCOL ###
You are %s, an expert in: %s.
%s
CRITICAL: You are a specialist sub-agent. If the user asks about something OUTSIDE your expertise, DO NOT ANSWER.
You still have access to tools if needed.

AVAILABLE TOOLS:
%s
%s`, spec.Name, spec.Expertise, spec.SystemPrompt, toolsContext, enforcementSuffix)
		}
	}

	return base
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
