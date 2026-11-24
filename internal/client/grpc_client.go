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
	"net"
	"path/filepath"
	"strings"
	"sync"
	"time"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/llm"
	"github.com/vraxter/vraxter/internal/security"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
)

// ClientTLSOptions holds mTLS 1.3 client certificate paths for remote TCP connections.
type ClientTLSOptions struct {
	CAPath     string
	CertPath   string
	KeyPath    string
	ServerName string
}

// HasPaths reports whether any TLS certificate/key path is configured.
func (o ClientTLSOptions) HasPaths() bool {
	return strings.TrimSpace(o.CAPath) != "" || strings.TrimSpace(o.CertPath) != "" || strings.TrimSpace(o.KeyPath) != ""
}

var (
	transportMu        sync.RWMutex
	defaultEndpoint    string
	defaultTLSOptions  ClientTLSOptions
)

// ConfigureDefaultTransport sets the process-wide default gRPC endpoint and mTLS 1.3 client options.
func ConfigureDefaultTransport(endpoint string, tlsOpts ClientTLSOptions) {
	transportMu.Lock()
	defer transportMu.Unlock()
	defaultEndpoint = strings.TrimSpace(endpoint)
	defaultTLSOptions = tlsOpts
}

// DefaultEndpoint returns the configured default gRPC endpoint, or falls back to
// config.Load().DefaultLocalEndpoint() (unix://<AppDir>/vraxter.sock on Unix, tcp://127.0.0.1:50051 on Windows).
func DefaultEndpoint() string {
	transportMu.RLock()
	ep := defaultEndpoint
	transportMu.RUnlock()
	if ep != "" {
		return ep
	}
	cfg := config.Load()
	return cfg.DefaultLocalEndpoint()
}

// DefaultTLSOptions returns the process-wide default mTLS 1.3 client options.
func DefaultTLSOptions() ClientTLSOptions {
	transportMu.RLock()
	defer transportMu.RUnlock()
	return defaultTLSOptions
}

func isLoopbackTCPHost(rawHost string) bool {
	h := strings.TrimSpace(rawHost)
	if splitH, _, err := net.SplitHostPort(h); err == nil {
		h = splitH
	}
	h = strings.TrimPrefix(strings.TrimSuffix(h, "]"), "[")
	if h == "" {
		return false
	}
	if strings.EqualFold(h, "localhost") || strings.EqualFold(h, "localhost.localdomain") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func dialDaemon(ctx context.Context, addr string, apiKey string, tlsOpts *ClientTLSOptions, block bool) (*grpc.ClientConn, error) {
	ep := strings.TrimSpace(addr)
	if ep == "" {
		ep = DefaultEndpoint()
	}

	dialOpts := []grpc.DialOption{
		grpc.WithPerRPCCredentials(NewAPICredentials(apiKey)),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: false,
		}),
	}
	if block {
		dialOpts = append(dialOpts, grpc.WithBlock())
	}

	// 1. Unix Domain Socket transport ("unix:///path/to/vraxter.sock" or "unix:/path/to/vraxter.sock")
	if strings.HasPrefix(ep, "unix://") || strings.HasPrefix(ep, "unix:") {
		sockPath := strings.TrimPrefix(strings.TrimPrefix(ep, "unix://"), "unix:")
		sockPath = filepath.Clean(strings.TrimSpace(sockPath))
		if sockPath == "" || sockPath == "." {
			return nil, fmt.Errorf("invalid unix socket address %q", addr)
		}
		dialOpts = append(dialOpts,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
			grpc.WithContextDialer(func(dialCtx context.Context, _ string) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(dialCtx, "unix", sockPath)
			}),
		)
		return grpc.DialContext(ctx, "passthrough:///unix-socket", dialOpts...)
	}

	// 2. TCP transport ("tcp://host:port" or "host:port")
	tcpAddr := strings.TrimPrefix(ep, "tcp://")
	host, _, err := net.SplitHostPort(tcpAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid gRPC TCP endpoint %q: %w", addr, err)
	}

	var effectiveTLS ClientTLSOptions
	if tlsOpts != nil {
		effectiveTLS = *tlsOpts
	} else {
		effectiveTLS = DefaultTLSOptions()
	}

	if effectiveTLS.HasPaths() {
		serverName := strings.TrimSpace(effectiveTLS.ServerName)
		if serverName == "" {
			serverName = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")
		}
		tlsCfg, err := security.LoadClientTLSConfig(effectiveTLS.CAPath, effectiveTLS.CertPath, effectiveTLS.KeyPath, serverName)
		if err != nil {
			return nil, fmt.Errorf("failed configuring mTLS 1.3 client: %w", err)
		}
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)))
	} else if !isLoopbackTCPHost(host) {
		return nil, fmt.Errorf("VRAXTER SHIELD: mTLS 1.3 credentials (--tls-ca, --tls-cert, --tls-key) are required for non-loopback TCP endpoint %s", tcpAddr)
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	return grpc.DialContext(ctx, tcpAddr, dialOpts...)
}

// GRPCClient manages the connection to the Vraxter Daemon
type GRPCClient struct {
	conn   *grpc.ClientConn
	client v1.AgentServiceClient
}

// NewGRPCClient connects to the daemon using default or configured transport credentials.
func NewGRPCClient(addr string, apiKey string) (*GRPCClient, error) {
	return NewGRPCClientWithTLS(addr, apiKey, nil)
}

// NewGRPCClientWithTLS connects to the daemon with explicit mTLS 1.3 options when provided.
func NewGRPCClientWithTLS(addr string, apiKey string, tlsOpts *ClientTLSOptions) (*GRPCClient, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()

	conn, err := dialDaemon(ctx, addr, apiKey, tlsOpts, true)
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

// GetConfig fetches active daemon isolation and security configuration.
func (c *GRPCClient) GetConfig(ctx context.Context) (*v1.ConfigResponse, error) {
	return c.client.GetConfig(ctx, &v1.GetConfigRequest{})
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
			case v1.ExecuteResponse_SKILL_APPROVAL_REQUEST:
				event.Type = llm.EventTypeSkillApprovalRequest
				event.SkillID = resp.SkillId
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
				select {
				case out <- event:
				case <-ctx.Done():
				}
				return
			}

			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
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
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
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
