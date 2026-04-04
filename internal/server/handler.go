package server

import (
	"fmt"

	v1 "github.com/patagonicrune/vraxter/api/v1"
	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/llm"
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
	ctx := stream.Context()

	// 1. Process intent via Engine (Returns a channel of events)
	events, err := h.engine.ProcessRawIntent(ctx, req.Query, "")
	if err != nil {
		return fmt.Errorf("engine failed to initialize stream: %w", err)
	}

	// 2. Pipe internal StreamEvents into gRPC ExecuteResponses
	for event := range events {
		resp := &v1.ExecuteResponse{
			Content: event.Content,
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
		}

		// 3. Send event to client
		if err := stream.Send(resp); err != nil {
			return fmt.Errorf("failed to send gRPC response: %w", err)
		}
	}

	return nil
}
