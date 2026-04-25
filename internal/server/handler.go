package server

import (
	"context"
	"fmt"
	"os"
	"time"

	v1 "github.com/patagonicrune/vraxter/api/v1"
	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/llm"
	"google.golang.org/grpc/metadata"
)

// AgentHandler implements the gRPC AgentService defined in agent.proto
type AgentHandler struct {
	v1.UnimplementedAgentServiceServer
	engine *core.Engine
}

func NewAgentHandler(engine *core.Engine) *AgentHandler {
	return &AgentHandler{engine: engine}
}

// Execute handles the gRPC stream for a natural language intent
func (h *AgentHandler) Execute(req *v1.ExecuteRequest, stream v1.AgentService_ExecuteServer) error {
	if req.Query == "SYSTEM_SHUTDOWN" {
		fmt.Println("🛑 Received Remote Shutdown Signal. Exiting...")
		go func() {
			time.Sleep(500 * time.Millisecond) // Give time for response to finalize
			os.Exit(0)
		}()
		return stream.Send(&v1.ExecuteResponse{
			Type:    v1.ExecuteResponse_DONE,
			Content: "Vraxter Daemon is shutting down...",
		})
	}

	ctx := stream.Context()
	specID := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if ids := md.Get("specialist-id"); len(ids) > 0 {
			specID = ids[0]
		}
	}

	// 1. Resolve Session Identity
	sessionID := req.ConversationId
	if sessionID == "" {
		sessionID = fmt.Sprintf("volatile-%d", time.Now().UnixNano())
	}

	// 2. Process intent via Engine (Returns a channel of events)
	events, err := h.engine.ProcessRawIntent(ctx, sessionID, req.Query, specID, req.ModelId)
	if err != nil {
		return fmt.Errorf("engine failed to initialize stream: %w", err)
	}

	// 2. Pipe internal StreamEvents into gRPC ExecuteResponses
	for event := range events {
		resp := &v1.ExecuteResponse{
			Content:       event.Content,
			ActiveModelId: h.engine.GetActiveModelID(sessionID),
		}

		switch event.Type {
		case llm.EventTypeToken:
			resp.Type = v1.ExecuteResponse_TOKEN
		case llm.EventTypeSkillCall:
			resp.Type = v1.ExecuteResponse_TOOL_CALL
			resp.SkillId = event.Content
		case llm.EventTypeError:
			resp.Type = v1.ExecuteResponse_ERROR
			if event.Err != nil {
				resp.Content = event.Err.Error()
			}
		case llm.EventTypeDone:
			resp.Type = v1.ExecuteResponse_DONE
		case llm.EventTypePlanProposal:
			resp.Type = v1.ExecuteResponse_PLAN_PROPOSAL
		case llm.EventTypeStatus:
			resp.Type = v1.ExecuteResponse_STATUS
		case llm.EventTypeSpecialistResult:
			resp.Type = v1.ExecuteResponse_SPECIALIST_RESULT
		}

		// 3. Send event to client
		if err := stream.Send(resp); err != nil {
			return fmt.Errorf("failed to send gRPC response: %w", err)
		}
	}

	return nil
}

func (h *AgentHandler) GetInfo(ctx context.Context, req *v1.GetInfoRequest) (*v1.GetInfoResponse, error) {
	modelID := h.engine.GetActiveModelID("")
	return &v1.GetInfoResponse{
		ModelId: modelID,
		Version: "v1.0.0",
		Status:  "READY",
	}, nil
}

func (h *AgentHandler) ListHistory(ctx context.Context, req *v1.ListHistoryRequest) (*v1.ListHistoryResponse, error) {
	limit := int(req.Limit)
	if limit <= 0 {
		limit = 20
	}

	convs, err := h.engine.ChatRepo.ListConversations(limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversations: %w", err)
	}

	resp := &v1.ListHistoryResponse{}
	for _, c := range convs {
		resp.Conversations = append(resp.Conversations, &v1.ConversationSummary{
			Id:           c.ID,
			Title:        c.Title,
			MessageCount: int32(c.MessageCount),
			CreatedAt:    c.CreatedAt.Format(time.RFC3339),
			UpdatedAt:    c.UpdatedAt.Format(time.RFC3339),
		})
	}
	return resp, nil
}

func (h *AgentHandler) GetConversation(ctx context.Context, req *v1.GetConversationRequest) (*v1.GetConversationResponse, error) {
	msgs, err := h.engine.ChatRepo.GetFullConversation(req.ConversationId)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	resp := &v1.GetConversationResponse{}
	for _, m := range msgs {
		resp.Messages = append(resp.Messages, &v1.HistoryMessage{
			Role:      m.Role,
			Content:   m.Content,
			Timestamp: m.Timestamp.Format(time.RFC3339),
		})
	}
	return resp, nil
}
