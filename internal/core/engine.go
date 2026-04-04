package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
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
)

// LLMResponse format expected uniformly from ALL providers
type LLMResponse struct {
	Action  string                 `json:"action"` // "chat" or "skill"
	SkillID string                 `json:"skill_id,omitempty"`
	Params  map[string]interface{} `json:"params,omitempty"`
	Content string                 `json:"content,omitempty"`
}

// Engine Orchestrator
type Engine struct {
	Store          *db.Store
	ModelsRepo     *db.ModelRepository
	ChatRepo       *db.ChatRepository
	SpecialistRepo *db.SpecialistRepository
	Router         *llm.Router
	SkillRegistry  *skills.Registry
	SkillRunner    *skills.Runner
	Coder          *services.CoderService

	// Performance Cache (P0/P1)
	mu                 sync.RWMutex
	cachedModels       []types.ModelConfig
	cachedSpecialists  []types.Specialist
	cachedModelsCtx    string
	cachedSpecsCtx     string
	cachedStaticPrompt string
	cacheExpiry        time.Time
	Verbose            bool
}

// NewEngine bootstraps The Brain
func NewEngine(store *db.Store, crypto *security.CryptoService, reg *skills.Registry, run *skills.Runner, coder *services.CoderService, verbose bool) (*Engine, error) {
	modelsRepo := db.NewModelRepository(store, crypto)
	chatRepo := db.NewChatRepository(store)

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

	return &Engine{
		Store:          store,
		ModelsRepo:     modelsRepo,
		ChatRepo:       chatRepo,
		SpecialistRepo: db.NewSpecialistRepository(store),
		Router:         router,
		SkillRegistry:  reg,
		SkillRunner:    run,
		Coder:          coder,
		Verbose:        verbose,
	}, nil
}

// refreshCache updates the in-memory metadata if expired (P0/P1)
func (e *Engine) refreshCache() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if time.Now().Before(e.cacheExpiry) {
		return nil
	}

	models, err := e.ModelsRepo.GetAllModelsPublic() // P2 Optimization: skip decrypt
	if err != nil {
		return err
	}

	specs, err := e.SpecialistRepo.GetAllSpecialists()
	if err != nil {
		return err
	}

	e.cachedModels = models
	e.cachedSpecialists = specs
	e.cacheExpiry = time.Now().Add(30 * time.Second)
	return nil
}

// ProcessRawIntent calls the LLM with Hybrid Streaming (CHAT / TOOL)
func (e *Engine) ProcessRawIntent(ctx context.Context, text string, specialistID string) (<-chan llm.StreamEvent, error) {
	out := make(chan llm.StreamEvent)

	intent := types.Intent{
		ID:        uuid.New().String(),
		Query:     text,
		Language:  "en",
		Timestamp: time.Now(),
	}

	if err := e.refreshCache(); err != nil {
		log.Printf("Engine: Cache refresh warning: %v", err)
	}

	e.mu.RLock()
	allModels := e.cachedModels
	allSpecialists := e.cachedSpecialists
	e.mu.RUnlock()

	orderedProviders := e.Router.GetOrderedProviders()
	if len(orderedProviders) == 0 {
		return nil, fmt.Errorf("no configured model providers available")
	}
	// The first entry is the active preferred model — used for building the prompt
	modelCfg := orderedProviders[0].Config

	// Dynamic Skills Context (Phase 12.11 - Prioritization)
	allSkills := e.SkillRegistry.GetAll()
	var officialSkills, communitySkills []string

	// Built-in Coder is always official and top priority
	officialSkills = append(officialSkills, "- ID: vraxter-coder [OFFICIAL] | Description: CREATE NEW TOOLS. Use this when the user needs a tool that doesn't exist. Params: name, description, logic (Go code).")

	if specialistID == "" {
		officialSkills = append(officialSkills, "- ID: vraxter-create-specialist [OFFICIAL] | Description: Create a Sub-Agent specialist. Params: name, expertise, model_id (optional).")
		officialSkills = append(officialSkills, "- ID: vraxter-delegate [OFFICIAL] | Description: Hand over user query to a specialist. Params: specialist_id.")
	} else {
		officialSkills = append(officialSkills, "- ID: vraxter-return-control [OFFICIAL] | Description: Hand control back to main supervisor Vraxter if query is out of your domain.")
	}
	for _, sl := range allSkills {
		label := ""
		if sl.IsOfficial {
			label = " [OFFICIAL]"
			officialSkills = append(officialSkills, fmt.Sprintf("- ID: %s%s | Description: %s", sl.ID, label, sl.Description))
		} else {
			communitySkills = append(communitySkills, fmt.Sprintf("- ID: %s | Description: %s (Trust Score: %.1f)", sl.ID, sl.Description, sl.Score))
		}
	}

	toolsContext := strings.Join(officialSkills, "\n")
	if len(communitySkills) > 0 {
		toolsContext += "\n\nCOMMUNITY TOOLS:\n" + strings.Join(communitySkills, "\n")
	}

	if len(allSkills) == 0 && len(officialSkills) == 1 {
		toolsContext = "Only 'vraxter-coder' is available."
	}

	const (
		markerChat = "<<<VRAX_CHAT>>>"
		markerTool = "<<<VRAX_TOOL>>>"
		markerCode = "<<<VRAX_CODE>>>"
	)

	// Dynamic Cache for strings (P1)
	e.mu.RLock()
	modelsCtxStr := e.cachedModelsCtx
	specsCtxStr := e.cachedSpecsCtx
	e.mu.RUnlock()

	if modelsCtxStr == "" || specsCtxStr == "" {
		// Rebuild if empty (should be rare if refreshCache worked)
		var mCtx strings.Builder
		for _, m := range allModels {
			status := "READY"
			if m.Provider == "ollama" && m.BaseURL == "" {
				status = "MISSING_CONFIG (Base URL Required)"
			} else if (m.Provider == "openai" || m.Provider == "gemini" || m.Provider == "anthropic") && m.APIKey == "" {
				status = "MISSING_CONFIG (API Key Required)"
			} else if !m.IsActive {
				status = "INACTIVE"
			}
			mCtx.WriteString(fmt.Sprintf("- ID: '%s' | Provider: %s | Model: %s | Status: %s | Capabilities: [%s] | Context: %d\n", m.ID, m.Provider, m.Model, status, m.Capabilities, m.ContextWindow))
		}
		modelsCtxStr = mCtx.String()
		if modelsCtxStr == "" {
			modelsCtxStr = "No models configured.\n"
		}

		var sCtx strings.Builder
		for _, s := range allSpecialists {
			sCtx.WriteString(fmt.Sprintf("- ID: '%s' | Name: %s | Expertise: %s\n", s.ID, s.Name, s.Expertise))
		}
		specsCtxStr = sCtx.String()
		if specsCtxStr == "" {
			specsCtxStr = "None.\n"
		}
	}

	systemPrompt := fmt.Sprintf(`### VRAXTER PROTOCOL: THE AGNOSTIC MULTI-PART DESIGNER ###
You are Vraxter, an autonomous agent engine. Your goal is to help the user by using tools, creating tools, or answering directly using your base knowledge.
CRITICAL RULES for Specialists:
A) DELEGATION: If the user's query heavily overlaps with an EXISTING SPECIALIST's expertise, you MUST invoke 'vraxter-delegate' IMMEDIATELY. Do not try to answer natively.
B) CREATION: NEVER use 'vraxter-create-specialist' on the first question of a new topic. You must answer natively. ONLY create a specialist if the user asks subsequent/follow-up questions about the same topic, OR if explicitly requested.
C) GENERAL: If you can answer a generic query directly, DO NOT CREATE OR USE TOOLS. Just reply naturally via chat.

EXISTING SPECIALISTS:
%s

AVAILABLE MODELS (Use these IDs if requested to assign a model to a specialist):
%s

AVAILABLE TOOLS:
%s

SKILL CREATION PROTOCOL (vraxter-coder / vraxter-coder-rust):
ONLY when a tool is absolutely required to fulfill a persistent system requirement, you MUST respond EXACTLY in 3 DISTINCT blocks WITHOUT markdown code blocks:
1. %s: A friendly confirmation.
2. %s: {"skill_id": "vraxter-coder", "params": {"name": "NAME", "description": "DESC"}} (or 'vraxter-coder-rust')
3. %s: The COMPLETE source file (main.go or main.rs). 
CRITICAL: You MUST use Go (standard lib only) for 'vraxter-coder'. NO PYTHON. NO MARKDOWN. NO BACKTICKS inside the code block.

GO BOILERPLATE (MANDATORY STRUCTURE):
package main
import ("encoding/json"; "os"; "fmt")

// NOTE: You can use HttpGet(url) for external data. 
// It is ALREADY PROVIDED by the Vraxter SDK in package main.

func main() {
	var req struct { Params struct { Intent map[string]interface{} "json:\"params\"" } "json:\"params\""; ID string "json:\"id\"" }
	if err := json.NewDecoder(os.Stdin).Decode(&req); err != nil { return	}
	
	// Example: data := HttpGet("https://api.exchangerate.com/v4/latest/USD")
	// Perform logic here...
	
	output := "Conversion result"
	fmt.Printf("{\"jsonrpc\":\"2.0\",\"id\":\"%%s\",\"result\":{\"status\":\"completed\",\"output\":\"%%s\"}}", req.ID, output)
}

TARGET: GOOS=wasip1, GOARCH=wasm / wasm32-wasip1.`, specsCtxStr, modelsCtxStr, toolsContext, markerChat, markerTool, markerCode)

	enforcementSuffix := "\n\n[CRITICAL SYSTEM DIRECTIVE: IF your answer REQUIRES YOU to GENERATE A TOOL, you MUST use the EXACT 3 blocks (<<<VRAX_CHAT>>>, <<<VRAX_TOOL>>>, <<<VRAX_CODE>>>) as instructed. NO MARKDOWN. The VRAX_TOOL block MUST be valid JSON. BUT IF you can answer directly without tools, DO NOT USE BLOCKS, JUST CHAT!]"

	// Apply Specialist Persona if requested
	if specialistID != "" {
		spec, err := e.SpecialistRepo.GetSpecialist(specialistID)
		if err == nil {
			systemPrompt = fmt.Sprintf(`### VRAXTER SPECIALIST PROTOCOL ###
You are %s, an expert in: %s.
%s
CRITICAL: You are a specialist sub-agent. If the user asks about something OUTSIDE your expertise, DO NOT ANSWER. Instead, ask the user to clear the agent flag.
You still have access to tools if needed.

AVAILABLE TOOLS:
%s
%s
`, spec.Name, spec.Expertise, spec.SystemPrompt, toolsContext, enforcementSuffix)
			// override model if specialist has a preferred one
			if spec.ModelID != "" {
				modelCfg.Model = spec.ModelID
				// Might need dynamic re-routing provider if ModelID is for another provider, but let's assume same provider/adapter context for now.
			}
		}
	}

	msgs := []llm.Message{
		{Role: "system", Content: systemPrompt},
	}

	// Load Chat History (Rolling Window)
	conversationID := "default"
	if specialistID != "" {
		c, err := e.ChatRepo.FindLatestConversationBySpecialist(specialistID)
		if err == nil && c != nil {
			conversationID = c.ID
		} else {
			// create new conversation for specialist
			conversationID = uuid.New().String()
			e.ChatRepo.CreateConversation(&types.Conversation{ID: conversationID, Title: "Specialist Chat", SpecialistID: specialistID})
		}
	} else {
		// Use a global default conversation for now if no specialist (or we could lookup default)
		// For simplicity, just load latest 20 msgs from default
	}

	history, _ := e.ChatRepo.GetMessagesByConversation(conversationID, 20)
	for _, historyMsg := range history {
		msgs = append(msgs, llm.Message{Role: historyMsg.Role, Content: historyMsg.Content})
	}

	msgs = append(msgs, llm.Message{Role: "user", Content: text + enforcementSuffix})

	// Save the current user message
	e.ChatRepo.SaveMessage(&types.Message{
		ID:             intent.ID,
		ConversationID: conversationID,
		Role:           "user",
		Content:        text,
		Timestamp:      time.Now(),
	})

	req := llm.CompletionRequest{
		Model:    modelCfg.Model,
		Messages: msgs,
	}

	go func() {
		defer close(out)

		var assistantMessage strings.Builder
		var lastErr error
		var streamFinished bool

		// Cascading Fallback: try each provider in priority order
		for i, entry := range orderedProviders {
			currentReq := req
			currentReq.Model = entry.Config.Model

			if streamFinished {
				break
			}

			if i == 0 {
				if e.Verbose {
					log.Printf("Engine: Using model %s", entry.Config.Model)
				}
			} else {
				msg := fmt.Sprintf("⚠️  [Fallback] Switching to %s", entry.Config.Model)
				out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n" + msg + "\n"}
			}

			stream, err := entry.Provider.StreamGenerate(ctx, currentReq)
			if err != nil {
				if e.Verbose {
					log.Printf("Model %s start failed: %v", entry.Config.Model, err)
				}
				lastErr = err
				continue
			}

			// Stream consumption with early error recovery
			parser := NewStreamParser(markerChat, markerTool, markerCode)
			isCommitted := false // True if we've sent actual tokens to the user

			for event := range stream {
				if event.Type == llm.EventTypeError {
					if !isCommitted {
						if e.Verbose {
							log.Printf("Model %s stream failed early: %v", entry.Config.Model, event.Err)
						}
						lastErr = event.Err
						goto nextProvider
					}
					out <- event
					return
				}

				if event.Type == llm.EventTypeToken {
					chatContent := parser.ProcessToken(event.Content)
					if chatContent != "" {
						isCommitted = true
						assistantMessage.WriteString(chatContent)
						out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: chatContent}
					}
				}

				if event.Type == llm.EventTypeDone {
					if tail := parser.Flush(); tail != "" {
						isCommitted = true
						assistantMessage.WriteString(tail)
						out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: tail}
					}

				toolPayload := parser.ToolPayload()
				codePayload := parser.CodePayload()
				if e.Verbose {
					log.Printf("Engine: Stream Done. ToolPayload: %d bytes, CodePayload: %d bytes", len(toolPayload), len(codePayload))
				}

				// 1. Process Meta-Tool (JSON)
				if len(toolPayload) > 0 {
					rawTool := strings.TrimSpace(toolPayload)
					if e.Verbose {
						log.Printf("Engine: Parsing Tool JSON: %s", rawTool)
					}
					var generic map[string]interface{}
					if err := json.Unmarshal([]byte(rawTool), &generic); err == nil {
						skillID, _ := generic["skill_id"].(string)

						// Resilience: If the LLM hallucinated the JSON schema but provided code, auto-detect it
						if skillID == "" && len(codePayload) > 0 {
							skillID = "vraxter-coder"
						}

						out <- llm.StreamEvent{Type: llm.EventTypeSkillCall, Content: skillID}

						// Save assistant tool call
						e.ChatRepo.SaveMessage(&types.Message{
							ID:             uuid.New().String(),
							ConversationID: conversationID,
							Role:           "skill",
							Content:        fmt.Sprintf("Invoked %s", skillID),
							Timestamp:      time.Now(),
						})

						// 2. Specialized Meta-skills
						if skillID == "vraxter-create-specialist" {
							name := ""
							expertise := ""
							modelID := ""
							if params, ok := generic["params"].(map[string]interface{}); ok {
								if n, ok := params["name"].(string); ok {
									name = n
								}
								if e, ok := params["expertise"].(string); ok {
									expertise = e
								}
								if m, ok := params["model_id"].(string); ok {
									modelID = m
								}
							}
							if name != "" && expertise != "" {
								s := types.Specialist{
									ID:        uuid.New().String(),
									Name:      name,
									Expertise: expertise,
									ModelID:   modelID,
								}
								e.SpecialistRepo.CreateSpecialist(s)
								out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n✅ Sub-Agent Specialist '%s' created! (ID: %s, Model: %s)", name, s.ID, modelID)}
							} else {
								out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n❌ Error: specific 'name' and 'expertise' required to create specialist."}
							}
							out <- llm.StreamEvent{Type: llm.EventTypeDone}
						} else if skillID == "vraxter-delegate" {
							specID := ""
							if params, ok := generic["params"].(map[string]interface{}); ok {
								if s, ok := params["specialist_id"].(string); ok {
									specID = s
								}
							}
							out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n🔄 Delegating query to Specialist %s...", specID)}
							// Currently we just acknowledge. For full multi-agent hop, engine needs recursion or client side re-trigger.
							out <- llm.StreamEvent{Type: llm.EventTypeDone}
						} else if skillID == "vraxter-return-control" {
							out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: "\n🔄 Returning control to Main Supervisor..."}
							out <- llm.StreamEvent{Type: llm.EventTypeDone}
						} else if (skillID == "vraxter-coder" || skillID == "vraxter-coder-rust") && len(codePayload) > 0 {

							// Resilience: Fuzzy extraction of name and desc from params or root
							name := "auto-tool"
							desc := "Auto-generated skill"

							if params, ok := generic["params"].(map[string]interface{}); ok {
								if n, ok := params["name"].(string); ok {
									name = n
								}
								if d, ok := params["description"].(string); ok {
									desc = d
								}
							} else {
								if n, ok := generic["name"].(string); ok {
									name = n
								} else if n, ok := generic["tool_name"].(string); ok {
									name = n
								}
								if d, ok := generic["description"].(string); ok {
									desc = d
								}
							}

							code := codePayload
							lang := "go"
							if skillID == "vraxter-coder-rust" {
								lang = "rust"
							}

							log.Printf("Engine: Invoking Coder [%s] for skill '%s' (%d bytes of code)", lang, name, len(code))
							err := e.Coder.CreateSkill(lang, name, desc, code)

							if err != nil {
								log.Printf("Engine: Coder Error: %v", err)
								errMsg := fmt.Sprintf("\n❌ Skill creation failed: %v\n\n[RECOVERY PROTOCOL: Please fix the issues and retry. Common errors: Missing 'package main', using non-Go code, or malformed imports. Use standard library ONLY.]", err)
								out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: errMsg}
								assistantMessage.WriteString(errMsg)
							} else {
								log.Printf("Engine: Coder Success!")
								out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n✅ Skill '%s' created and installed successfully!", name)}
							}
							out <- llm.StreamEvent{Type: llm.EventTypeDone}
						} else {
							// 3. Standard Execution
							manifest, err := e.SkillRegistry.FindSkill(skillID)
							if err == nil {
								params, _ := generic["params"].(map[string]interface{})
								result, err := e.SkillRunner.Execute(ctx, *manifest, params)
								if err == nil {
									out <- llm.StreamEvent{Type: llm.EventTypeToken, Content: fmt.Sprintf("\n[SALIDA DE %s: %s]", skillID, result.Output)}
								}
							}
						}
						}
					}
					
					streamFinished = true
					out <- event
					break
				}
			}

		nextProvider:
		}

		if !streamFinished {
			out <- llm.StreamEvent{Type: llm.EventTypeError, Err: fmt.Errorf("all models failed. Last error: %v", lastErr)}
			return
		}

		finalMsg := assistantMessage.String()
		if finalMsg != "" {
			e.ChatRepo.SaveMessage(&types.Message{
				ID: uuid.New().String(), ConversationID: conversationID, Role: "assistant", Content: strings.TrimSpace(finalMsg), Timestamp: time.Now(),
			})
		}
	}()

	return out, nil
}
