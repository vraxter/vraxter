// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package client

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/llm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

// GRPCClient manages the connection to the Vraxter Daemon
type GRPCClient struct {
	conn   *grpc.ClientConn
	client v1.AgentServiceClient
}

// NewGRPCClient connects to the local daemon
func NewGRPCClient(addr string, apiKey string) (*GRPCClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(NewAPICredentials(apiKey)),
		grpc.WithBlock(),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: false,
		}),
	)
	if err != nil {
		return nil, err
	}

	return &GRPCClient{
		conn:   conn,
		client: v1.NewAgentServiceClient(conn),
	}, nil
}

// Close gracefully shuts down the gRPC connection
func (c *GRPCClient) Close() error {
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// GetInfo returns daemon metadata
func (c *GRPCClient) GetInfo(ctx context.Context) (map[string]string, error) {
	resp, err := c.client.GetInfo(ctx, &v1.GetInfoRequest{})
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"model_id": resp.ModelId,
		"version":  resp.Version,
		"status":   resp.Status,
	}, nil
}

// ExecuteStream sends a query and returns a generic LLM StreamEvent channel
func (c *GRPCClient) ExecuteStream(ctx context.Context, query string, conversationID string, specialistID string, modelID string) (<-chan llm.StreamEvent, error) {
	if specialistID != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "specialist-id", specialistID)
	}
	req := &v1.ExecuteRequest{
		Query:          query,
		ConversationId: conversationID,
		SpecialistId:   specialistID,
		ModelId:        modelID,
	}
	stream, err := c.client.Execute(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan llm.StreamEvent)

	go func() {
		defer close(out)

		for {
			resp, err := stream.Recv()
			if err != nil {
				return
			}

			event := llm.StreamEvent{
				Content:       resp.Content,
				ActiveModelID: resp.ActiveModelId,
			}

			switch resp.Type {
			case v1.ExecuteResponse_TOKEN:
				event.Type = llm.EventTypeToken
			case v1.ExecuteResponse_TOOL_CALL:
				event.Type = llm.EventTypeSkillCall
				if resp.SkillId != "" {
					event.Content = resp.SkillId
				}
			case v1.ExecuteResponse_ERROR:
				event.Type = llm.EventTypeError
				event.Err = fmt.Errorf("%s", resp.Content)
			case v1.ExecuteResponse_PLAN_PROPOSAL:
				event.Type = llm.EventTypePlanProposal
			case v1.ExecuteResponse_STATUS:
				event.Type = llm.EventTypeStatus
			case v1.ExecuteResponse_SPECIALIST_RESULT:
				event.Type = llm.EventTypeSpecialistResult
			case v1.ExecuteResponse_DONE:
				event.Type = llm.EventTypeDone
				out <- event
				return
			}

			out <- event
		}
	}()

	return out, nil
}

// SubscribeEvents attaches to the engine's global event bus to receive asynchronous job notifications
func (c *GRPCClient) SubscribeEvents(ctx context.Context, clientID string) (<-chan *v1.SystemEvent, error) {
	req := &v1.SubscribeEventsRequest{ClientId: clientID}
	stream, err := c.client.SubscribeEvents(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan *v1.SystemEvent)

	go func() {
		defer close(out)
		for {
			event, err := stream.Recv()
			if err != nil {
				return // Stream closed or errored
			}
			out <- event
		}
	}()

	return out, nil
}

// GetActiveJobs queries the engine for a list of all currently tracked background daemons
func (c *GRPCClient) GetActiveJobs(ctx context.Context) ([]*v1.JobStatus, error) {
	resp, err := c.client.GetActiveJobs(ctx, &v1.GetActiveJobsRequest{})
	if err != nil {
		return nil, err
	}
	return resp.Jobs, nil
}

// ConversationSummary is a lightweight view of a conversation for listing
type ConversationSummary struct {
	ID           string
	Title        string
	MessageCount int
	CreatedAt    string
	UpdatedAt    string
}

// HistoryMessage is a single message entry for conversation replay
type HistoryMessage struct {
	Role      string
	Content   string
	Timestamp string
}

// ListHistory returns the most recent conversations
func (c *GRPCClient) ListHistory(ctx context.Context, limit int) ([]ConversationSummary, error) {
	resp, err := c.client.ListHistory(ctx, &v1.ListHistoryRequest{Limit: int32(limit)})
	if err != nil {
		return nil, err
	}

	var result []ConversationSummary
	for _, conv := range resp.Conversations {
		result = append(result, ConversationSummary{
			ID:           conv.Id,
			Title:        conv.Title,
			MessageCount: int(conv.MessageCount),
			CreatedAt:    conv.CreatedAt,
			UpdatedAt:    conv.UpdatedAt,
		})
	}
	return result, nil
}

// GetConversation returns all messages for a given conversation ID
func (c *GRPCClient) GetConversation(ctx context.Context, conversationID string) ([]HistoryMessage, error) {
	resp, err := c.client.GetConversation(ctx, &v1.GetConversationRequest{ConversationId: conversationID})
	if err != nil {
		return nil, err
	}

	var result []HistoryMessage
	for _, msg := range resp.Messages {
		result = append(result, HistoryMessage{
			Role:      msg.Role,
			Content:   msg.Content,
			Timestamp: msg.Timestamp,
		})
	}
	return result, nil
}
