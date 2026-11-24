// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/llm/anthropic"
	"github.com/vraxter/vraxter/internal/llm/google"
	"github.com/vraxter/vraxter/internal/llm/openai"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/skills"
	"github.com/vraxter/vraxter/pkg/interfaces"
	"github.com/vraxter/vraxter/pkg/types"
)

func TestLote1_5_AES256GCMAtRestEncryption_ColumnsBackupsAndSealedDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "vraxter.db")

	store, err := db.NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore failed: %v", err)
	}

	// Verify db_master.key was generated atomically with 32 bytes and 0600 permissions
	masterKeyPath := filepath.Join(tmpDir, security.DefaultDBMasterKeyFilename)
	keyBytes, err := os.ReadFile(masterKeyPath)
	if err != nil {
		store.Close()
		t.Fatalf("expected db_master.key at %s: %v", masterKeyPath, err)
	}
	if len(keyBytes) != 32 {
		store.Close()
		t.Fatalf("expected 32-byte db_master.key, got %d bytes", len(keyBytes))
	}
	if runtime.GOOS != "windows" {
		fi, err := os.Stat(masterKeyPath)
		if err != nil {
			store.Close()
			t.Fatalf("stat db_master.key: %v", err)
		}
		if fi.Mode().Perm() != 0600 {
			store.Close()
			t.Fatalf("expected db_master.key mode 0600, got %o", fi.Mode().Perm())
		}
	}

	secretConvTitle := "CLASSIFIED_OPERATION_ORION_TITLE"
	secretConvSummary := "CLASSIFIED_SUMMARY_SATELLITE_TELEMETRY"
	secretMsgContent := "CLASSIFIED_MESSAGE_PAYLOAD_ALPHA_BRAVO"
	secretMemoryText := "CLASSIFIED_RAG_MEMORY_VECTOR_TEXT"
	secretSpecPrompt := "CLASSIFIED_SPECIALIST_SYSTEM_PROMPT_ZERO_TRUST"

	chatRepo := db.NewChatRepository(store)
	if err := chatRepo.CreateConversation(&types.Conversation{
		ID:      "conv-secret-1",
		Title:   secretConvTitle,
		Summary: secretConvSummary,
	}); err != nil {
		store.Close()
		t.Fatalf("CreateConversation failed: %v", err)
	}
	if err := chatRepo.SaveMessage(&types.Message{
		ID:             "msg-secret-1",
		ConversationID: "conv-secret-1",
		Role:           "user",
		Content:        secretMsgContent,
		TokensUsed:     42,
	}); err != nil {
		store.Close()
		t.Fatalf("SaveMessage failed: %v", err)
	}

	memRepo := db.NewMemoryRepository(store)
	if err := memRepo.SaveEmbedding("conv-secret-1", secretMemoryText, []float32{0.25, 0.5, 0.75, 1.0}); err != nil {
		store.Close()
		t.Fatalf("SaveEmbedding failed: %v", err)
	}

	specRepo := db.NewSpecialistRepository(store)
	if err := specRepo.CreateSpecialist(types.Specialist{
		ID:           "spec-secret-1",
		Name:         "Telemetry_Lead",
		Expertise:    "CLASSIFIED_EXPERTISE_AVIONICS",
		ModelID:      "local-model",
		SystemPrompt: secretSpecPrompt,
	}); err != nil {
		store.Close()
		t.Fatalf("CreateSpecialist failed: %v", err)
	}

	// Verify transparent decryption through repositories
	conv, err := chatRepo.FindConversation("conv-secret-1")
	if err != nil || conv == nil {
		store.Close()
		t.Fatalf("FindConversation failed: %v", err)
	}
	if conv.Title != secretConvTitle || conv.Summary != secretConvSummary {
		store.Close()
		t.Fatalf("unexpected decrypted conversation: %+v", conv)
	}

	msgs, err := chatRepo.GetMessagesByConversation("conv-secret-1", 10)
	if err != nil || len(msgs) != 1 || msgs[0].Content != secretMsgContent {
		store.Close()
		t.Fatalf("unexpected decrypted messages: %+v (err=%v)", msgs, err)
	}

	memResults, err := memRepo.SearchSimilar("conv-secret-1", []float32{0.25, 0.5, 0.75, 1.0}, 5)
	if err != nil || len(memResults) != 1 || memResults[0].Node.TextContent != secretMemoryText {
		store.Close()
		t.Fatalf("unexpected decrypted memory results: %+v (err=%v)", memResults, err)
	}

	spec, err := specRepo.GetSpecialist("spec-secret-1")
	if err != nil || spec == nil || spec.SystemPrompt != secretSpecPrompt {
		store.Close()
		t.Fatalf("unexpected decrypted specialist: %+v (err=%v)", spec, err)
	}

	// Create encrypted backup (.db.enc) and verify it contains zero plaintext strings
	encBackupPath, err := store.BackupEncrypted()
	if err != nil {
		store.Close()
		t.Fatalf("BackupEncrypted failed: %v", err)
	}
	encBackupBytes, err := os.ReadFile(encBackupPath)
	if err != nil {
		store.Close()
		t.Fatalf("read encrypted backup: %v", err)
	}
	if !bytes.HasPrefix(encBackupBytes, []byte(security.EncryptedBlobMagic)) {
		store.Close()
		t.Fatalf("expected encrypted backup to start with %s header", security.EncryptedBlobMagic)
	}
	for _, secret := range []string{secretConvTitle, secretConvSummary, secretMsgContent, secretMemoryText, secretSpecPrompt} {
		if bytes.Contains(encBackupBytes, []byte(secret)) {
			store.Close()
			t.Fatalf("plaintext secret %q leaked into encrypted backup %s", secret, encBackupPath)
		}
	}

	// Flush WAL and close store, then inspect raw SQLite file bytes on disk to confirm no plaintext strings appear
	if err := store.Close(); err != nil {
		t.Fatalf("store.Close failed: %v", err)
	}

	for _, fileOnDisk := range []string{dbPath, dbPath + "-wal"} {
		rawDisk, err := os.ReadFile(fileOnDisk)
		if err != nil {
			continue
		}
		for _, secret := range []string{secretConvTitle, secretConvSummary, secretMsgContent, secretMemoryText, secretSpecPrompt, "CLASSIFIED_EXPERTISE_AVIONICS"} {
			if bytes.Contains(rawDisk, []byte(secret)) {
				t.Fatalf("plaintext secret %q found on disk in %s", secret, fileOnDisk)
			}
		}
	}

	// Subtest: Full database file sealing when encrypt_db_at_rest: true
	t.Run("FullDatabaseSealingOnCloseAndUnsealOnOpen", func(t *testing.T) {
		sealedDir := t.TempDir()
		sealedDBPath := filepath.Join(sealedDir, "vraxter.db")

		sealedStore, err := db.NewStoreWithOptions(sealedDBPath, db.StoreOptions{
			EncryptAtRest: true,
		})
		if err != nil {
			t.Fatalf("NewStoreWithOptions(EncryptAtRest=true) failed: %v", err)
		}

		sealedChat := db.NewChatRepository(sealedStore)
		if err := sealedChat.CreateConversation(&types.Conversation{
			ID:    "conv-sealed",
			Title: "SEALED_DB_CONVERSATION",
		}); err != nil {
			sealedStore.Close()
			t.Fatalf("CreateConversation in sealed store: %v", err)
		}
		if err := sealedChat.SaveMessage(&types.Message{
			ID:             "msg-sealed",
			ConversationID: "conv-sealed",
			Role:           "assistant",
			Content:        "SEALED_DB_MESSAGE_PAYLOAD",
		}); err != nil {
			sealedStore.Close()
			t.Fatalf("SaveMessage in sealed store: %v", err)
		}

		// Close the store: must seal to vraxter.db.enc and leave ZERO plaintext .db files on disk
		if err := sealedStore.Close(); err != nil {
			t.Fatalf("sealedStore.Close failed: %v", err)
		}

		for _, forbiddenPlain := range []string{
			sealedDBPath,
			sealedDBPath + "-wal",
			sealedDBPath + "-shm",
			filepath.Join(sealedDir, ".vraxter.db.working"),
		} {
			if _, err := os.Stat(forbiddenPlain); !os.IsNotExist(err) {
				t.Fatalf("expected plaintext DB artifact %s to be removed on Close()", forbiddenPlain)
			}
		}

		encFile := sealedDBPath + ".enc"
		rawSealed, err := os.ReadFile(encFile)
		if err != nil {
			t.Fatalf("expected sealed archive at %s: %v", encFile, err)
		}
		if !bytes.HasPrefix(rawSealed, []byte(security.EncryptedBlobMagic)) {
			t.Fatalf("expected sealed archive %s to start with %s", encFile, security.EncryptedBlobMagic)
		}
		if bytes.Contains(rawSealed, []byte("SQLite format 3")) || bytes.Contains(rawSealed, []byte("SEALED_DB_MESSAGE_PAYLOAD")) {
			t.Fatalf("expected sealed archive %s to be fully encrypted with zero SQLite header or plaintext", encFile)
		}

		// Reopen the sealed store and verify all data is restored cleanly
		reopenedStore, err := db.NewStoreWithOptions(sealedDBPath, db.StoreOptions{
			EncryptAtRest: true,
		})
		if err != nil {
			t.Fatalf("reopening sealed store failed: %v", err)
		}
		defer reopenedStore.Close()

		reopenedChat := db.NewChatRepository(reopenedStore)
		restoredMsgs, err := reopenedChat.GetFullConversation("conv-sealed")
		if err != nil || len(restoredMsgs) != 1 || restoredMsgs[0].Content != "SEALED_DB_MESSAGE_PAYLOAD" {
			t.Fatalf("unexpected restored messages from sealed DB: %+v (err=%v)", restoredMsgs, err)
		}
	})
}

type mockFallbackProvider struct {
	events []interfaces.StreamEvent
}

func (m *mockFallbackProvider) Generate(ctx context.Context, req interfaces.CompletionRequest) (interfaces.CompletionResponse, error) {
	return interfaces.CompletionResponse{}, nil
}
func (m *mockFallbackProvider) StreamGenerate(ctx context.Context, req interfaces.CompletionRequest) (<-chan interfaces.StreamEvent, error) {
	ch := make(chan interfaces.StreamEvent, len(m.events))
	for _, ev := range m.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}
func (m *mockFallbackProvider) Embed(ctx context.Context, model string, texts []string) ([][]float32, error) {
	return nil, nil
}
func (m *mockFallbackProvider) Discover(ctx context.Context) ([]interfaces.ModelMetadata, error) {
	return nil, nil
}
func (m *mockFallbackProvider) GetModelDetails(ctx context.Context, modelID string) (map[string]interface{}, error) {
	return nil, nil
}
func (m *mockFallbackProvider) CheckHealth(ctx context.Context) error {
	return nil
}

func TestLote1_5_DeterministicParamsAndNativeToolCallingWithVRXFallback(t *testing.T) {
	// 1. Verify OpenAI/Ollama adapter propagates Temperature=0.0, Seed=42, MaxTokens, TopP, Tools and parses native tool_calls
	var capturedOpenAIBody map[string]interface{}
	openAISrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedOpenAIBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{
				"message": {
					"content": "",
					"tool_calls": [{
						"id": "call_123",
						"type": "function",
						"function": {
							"name": "aero.telemetry_ingest",
							"arguments": "{\"sensor\":\"imu_01\",\"window_sec\":30}"
						}
					}]
				}
			}]
		}`))
	}))
	defer openAISrv.Close()

	oaAdapter := openai.NewAdapter("test-key", openAISrv.URL)
	req := llm.CompletionRequest{
		Model:       "qwen2.5-coder:14b",
		Messages:    []llm.Message{{Role: "user", Content: "Ingest IMU telemetry"}},
		Temperature: llm.Float64Ptr(0.0),
		Seed:        llm.Int64Ptr(42),
		MaxTokens:   512,
		TopP:        llm.Float64Ptr(1.0),
		Tools: []llm.ToolDefinition{
			{
				Name:             "aero.telemetry_ingest",
				Description:      "Ingest aerospace sensor telemetry",
				ParametersSchema: json.RawMessage(`{"type":"object","properties":{"sensor":{"type":"string"}}}`),
			},
		},
	}

	resp, err := oaAdapter.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("OpenAI Generate failed: %v", err)
	}
	if tempVal, ok := capturedOpenAIBody["temperature"].(float64); !ok || tempVal != 0.0 {
		t.Fatalf("expected temperature=0.0 in OpenAI request JSON, got %v (%T)", capturedOpenAIBody["temperature"], capturedOpenAIBody["temperature"])
	}
	if seedVal, ok := capturedOpenAIBody["seed"].(float64); !ok || int64(seedVal) != 42 {
		t.Fatalf("expected seed=42 in OpenAI request JSON, got %v", capturedOpenAIBody["seed"])
	}
	if toolsArr, ok := capturedOpenAIBody["tools"].([]interface{}); !ok || len(toolsArr) != 1 {
		t.Fatalf("expected 1 tool definition in OpenAI request JSON, got %v", capturedOpenAIBody["tools"])
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "aero.telemetry_ingest" || resp.ToolCalls[0].Arguments["sensor"] != "imu_01" {
		t.Fatalf("unexpected parsed native ToolCalls from OpenAI: %+v", resp.ToolCalls)
	}

	// 2. Verify Google Gemini adapter propagates generationConfig (temperature=0, seed=42) + tools and parses functionCall
	var capturedGeminiBody map[string]interface{}
	geminiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &capturedGeminiBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{
				"content": {
					"parts": [{
						"functionCall": {
							"name": "sec.pcap_entropy_scan",
							"args": {"pcap_file": "capture.pcap"}
						}
					}]
				}
			}]
		}`))
	}))
	defer geminiSrv.Close()

	gemAdapter := google.NewAdapter("gem-key", geminiSrv.URL)
	gemResp, err := gemAdapter.Generate(context.Background(), req)
	if err != nil {
		t.Fatalf("Google Generate failed: %v", err)
	}
	genCfg, ok := capturedGeminiBody["generationConfig"].(map[string]interface{})
	if !ok || genCfg["temperature"] != float64(0) || genCfg["seed"] != float64(42) {
		t.Fatalf("expected deterministic generationConfig in Gemini request, got %+v", capturedGeminiBody["generationConfig"])
	}
	if len(gemResp.ToolCalls) != 1 || gemResp.ToolCalls[0].Name != "sec.pcap_entropy_scan" {
		t.Fatalf("unexpected parsed native ToolCalls from Gemini: %+v", gemResp.ToolCalls)
	}

	// 3. Verify StreamCoordinator handles Native Tool Calling AND falls back automatically to VRX textual protocol
	t.Run("StreamCoordinatorNativeAndVRXFallback", func(t *testing.T) {
		// Native tool call via StreamEvent
		nativeTC := &interfaces.ToolCall{
			ID:        "tc-1",
			Name:      "vraxter-read-file",
			Arguments: map[string]interface{}{"path": "README.md"},
		}
		nativeProvider := &mockFallbackProvider{
			events: []interfaces.StreamEvent{
				{Type: interfaces.EventTypeSkillCall, SkillID: "vraxter-read-file", ToolCall: nativeTC},
				{Type: interfaces.EventTypeDone},
			},
		}
		routerNative := llm.NewRouterFromEntries([]llm.RouterEntry{
			{
				Provider: nativeProvider,
				Config:   types.ModelConfig{ID: "m1", ProviderID: "p1", Model: "native-model"},
			},
		})
		scNative := core.NewStreamCoordinator(routerNative, false)
		outCh := make(chan llm.StreamEvent, 10)
		queueCh := make(chan types.Event, 10)
		if err := scNative.Run(context.Background(), outCh, queueCh, llm.CompletionRequest{Model: "native-model"}, "[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]"); err != nil {
			t.Fatalf("StreamCoordinator native run failed: %v", err)
		}
		close(queueCh)
		var gotNativeToolPayload string
		for ev := range queueCh {
			if ev.Type == types.EventTypeToolCall {
				if m, ok := ev.Payload.(map[string]string); ok {
					gotNativeToolPayload = m["tool_payload"]
				}
			}
		}
		if !strings.Contains(gotNativeToolPayload, `"skill_id":"vraxter-read-file"`) {
			t.Fatalf("expected native tool call dispatched to pipeline, got: %q", gotNativeToolPayload)
		}

		// Fallback to VRX textual protocol ([VRAX_TOOL]) when provider emits only text tokens
		vrxProvider := &mockFallbackProvider{
			events: []interfaces.StreamEvent{
				{Type: interfaces.EventTypeToken, Content: "[VRAX_CHAT] Reading file...\n[VRAX_TOOL] {\"skill_id\":\"vraxter-list-dir\",\"params\":{\"path\":\".\"}}"},
				{Type: interfaces.EventTypeDone},
			},
		}
		routerVRX := llm.NewRouterFromEntries([]llm.RouterEntry{
			{
				Provider: vrxProvider,
				Config:   types.ModelConfig{ID: "m2", ProviderID: "p2", Model: "legacy-vrx-model"},
			},
		})
		scVRX := core.NewStreamCoordinator(routerVRX, false)
		outCh2 := make(chan llm.StreamEvent, 10)
		queueCh2 := make(chan types.Event, 10)
		if err := scVRX.Run(context.Background(), outCh2, queueCh2, llm.CompletionRequest{Model: "legacy-vrx-model"}, "[VRAX_CHAT]", "[VRAX_TOOL]", "[VRAX_CODE]"); err != nil {
			t.Fatalf("StreamCoordinator VRX fallback run failed: %v", err)
		}
		close(queueCh2)
		var gotVRXToolPayload string
		for ev := range queueCh2 {
			if ev.Type == types.EventTypeToolCall {
				if m, ok := ev.Payload.(map[string]string); ok {
					gotVRXToolPayload = m["tool_payload"]
				}
			}
		}
		if !strings.Contains(gotVRXToolPayload, `"skill_id":"vraxter-list-dir"`) {
			t.Fatalf("expected VRX fallback tool call dispatched to pipeline, got: %q", gotVRXToolPayload)
		}
	})
}

func TestLote1_5_AnthropicRealSSEAndCircuitBreakerStateTransitions(t *testing.T) {
	// 1. Real Anthropic SSE stream simulation with incremental text_delta and input_json_delta events
	sseSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)

		frames := []string{
			"event: message_start\ndata: {\"type\":\"message_start\"}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Analyzing \"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"telemetry...\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n",
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_01\",\"name\":\"aero.fault_classifier\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"severity\\\":\"}}\n\n",
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"critical\\\"}\"}}\n\n",
			"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n",
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		}
		for _, frame := range frames {
			_, _ = fmt.Fprint(w, frame)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer sseSrv.Close()

	anthAdapter := anthropic.NewAdapter("anth-test-key", sseSrv.URL)
	stream, err := anthAdapter.StreamGenerate(context.Background(), llm.CompletionRequest{
		Model:       "claude-3-5-sonnet-20240620",
		Messages:    []llm.Message{{Role: "user", Content: "Run fault classifier"}},
		Temperature: llm.Float64Ptr(0.0),
	})
	if err != nil {
		t.Fatalf("Anthropic StreamGenerate failed: %v", err)
	}

	var tokens []string
	var receivedTool *interfaces.ToolCall
	var sawDone bool

	for ev := range stream {
		switch ev.Type {
		case interfaces.EventTypeToken:
			tokens = append(tokens, ev.Content)
		case interfaces.EventTypeSkillCall:
			receivedTool = ev.ToolCall
		case interfaces.EventTypeDone:
			sawDone = true
		case interfaces.EventTypeError:
			t.Fatalf("unexpected Anthropic SSE error: %v", ev.Err)
		}
	}

	if len(tokens) != 2 || strings.Join(tokens, "") != "Analyzing telemetry..." {
		t.Fatalf("expected 2 incremental SSE text_delta tokens ('Analyzing telemetry...'), got %v", tokens)
	}
	if receivedTool == nil || receivedTool.Name != "aero.fault_classifier" || receivedTool.Arguments["severity"] != "critical" {
		t.Fatalf("expected assembled SSE input_json_delta tool_use call, got %+v", receivedTool)
	}
	if !sawDone {
		t.Fatalf("expected EventTypeDone after message_stop")
	}

	// 2. CircuitBreaker deterministic state machine: Closed -> Open -> HalfOpen -> Closed
	now := time.Date(2026, 11, 23, 17, 0, 0, 0, time.UTC)
	cb := llm.NewCircuitBreaker(llm.CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Cooldown:         10 * time.Second,
		JitterFraction:   0, // deterministic cooldown
		Clock:            func() time.Time { return now },
	})

	if cb.State() != llm.CircuitClosed {
		t.Fatalf("expected initial state Closed, got %s", cb.State())
	}

	// Record 3 consecutive failures -> transitions Closed -> Open
	cb.RecordFailure()
	cb.RecordFailure()
	if cb.State() != llm.CircuitClosed {
		t.Fatalf("expected state Closed after 2/3 failures, got %s", cb.State())
	}
	cb.RecordFailure()
	if cb.State() != llm.CircuitOpen {
		t.Fatalf("expected state Open after 3/3 consecutive failures, got %s", cb.State())
	}

	// While Open and before cooldown, Allow() must reject fast
	if cb.Allow() {
		t.Fatalf("expected Allow() == false while circuit is Open before cooldown")
	}
	if cb.Metrics().RejectedCount != 1 {
		t.Fatalf("expected RejectedCount == 1, got %d", cb.Metrics().RejectedCount)
	}

	// Advance clock past 10s cooldown -> Allow() transitions Open -> HalfOpen
	now = now.Add(11 * time.Second)
	if !cb.Allow() {
		t.Fatalf("expected Allow() == true after cooldown elapsed")
	}
	if cb.State() != llm.CircuitHalfOpen {
		t.Fatalf("expected state HalfOpen on probe request, got %s", cb.State())
	}

	// Probe succeeds -> transitions HalfOpen -> Closed
	cb.RecordSuccess()
	if cb.State() != llm.CircuitClosed {
		t.Fatalf("expected state Closed after successful HalfOpen probe, got %s", cb.State())
	}
	if cb.Metrics().ConsecutiveFailures != 0 {
		t.Fatalf("expected ConsecutiveFailures reset to 0, got %d", cb.Metrics().ConsecutiveFailures)
	}
}

// infiniteLoopWASIModule constructs a valid WASM binary whose _start function enters
// an infinite loop (`loop ... br 0 ... end`) without making any WASI syscalls.
func infiniteLoopWASIModule() []byte {
	return []byte{
		0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00, // magic + version
		0x01, 0x04, 0x01, 0x60, 0x00, 0x00, // type section: () -> ()
		0x03, 0x02, 0x01, 0x00, // func section: func 0 uses type 0
		0x05, 0x03, 0x01, 0x00, 0x01, // memory section: min 1 page
		0x07, 0x13, 0x02, // export section: memory (mem 0), _start (func 0)
		0x06, 'm', 'e', 'm', 'o', 'r', 'y', 0x02, 0x00,
		0x06, '_', 's', 't', 'a', 'r', 't', 0x00, 0x00,
		// Code section: 1 function body: 0 locals, loop (empty blocktype 0x40), br 0, end, end
		0x0a, 0x09, 0x01,
		0x07, 0x00,
		0x03, 0x40, // loop (result empty)
		0x0c, 0x00, // br 0
		0x0b, // end loop
		0x0b, // end func
	}
}

func TestLote1_5_DeterministicWASMCPUFuelTrapOnInfiniteLoop(t *testing.T) {
	tmpDir := t.TempDir()
	wasmPath := filepath.Join(tmpDir, "infinite-loop.wasm")
	if err := os.WriteFile(wasmPath, infiniteLoopWASIModule(), 0600); err != nil {
		t.Fatalf("write infinite-loop.wasm: %v", err)
	}

	const configuredFuelBudget int64 = 250

	manifest := types.SkillManifest{
		ID:         "infinite-loop",
		Name:       "Infinite Loop Skill",
		Version:    "1.0.0",
		Command:    wasmPath,
		Tier:       types.Tier3Unverified,
		IsOfficial: false,
		ManifestV1: &types.SkillManifestV1{
			ManifestVersion: types.ManifestVersionV1,
			ID:              "infinite-loop",
			Name:            "Infinite Loop Skill",
			Version:         "1.0.0",
			Module: types.ModuleSpec{
				File: "infinite-loop.wasm",
				ABI:  "wasip1",
			},
			Limits: &types.LimitsSpec{
				MemoryMB:     16,
				MaxRuntimeMS: 30000, // Long 30s timeout to prove termination is driven by deterministic CPU fuel, not wall clock
				MaxCPUFuel:   configuredFuelBudget,
			},
		},
	}

	runner := skills.NewRunner()
	start := time.Now()
	_, err := runner.Execute(context.Background(), manifest, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("expected infinite loop WASM skill to trap with ErrCPUFuelExhausted, but execution succeeded")
	}
	if !errors.Is(err, skills.ErrCPUFuelExhausted) {
		t.Fatalf("expected error wrapping skills.ErrCPUFuelExhausted, got: %v", err)
	}
	expectedConsumptionMsg := fmt.Sprintf("consumed %d / %d fuel units", configuredFuelBudget, configuredFuelBudget)
	if !strings.Contains(err.Error(), expectedConsumptionMsg) {
		t.Fatalf("expected exact fuel budget report %q in error, got: %v", expectedConsumptionMsg, err)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("expected deterministic CPU fuel trap well before wall-clock timeout, took %v", elapsed)
	}
}
