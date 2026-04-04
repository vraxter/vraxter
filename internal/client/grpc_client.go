package client

import (
	"context"
	"fmt"
	"time"

	v1 "github.com/patagonicrune/vraxter/api/v1"
	"github.com/patagonicrune/vraxter/internal/llm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// gRPCClient manages the connection to the Vraxter Daemon
type gRPCClient struct {
	conn   *grpc.ClientConn
	client v1.AgentServiceClient
}

// NewGRPCClient connects to the local daemon
func NewGRPCClient(addr string) (*gRPCClient, error) {
	// Fast Dial: if it's not there, we want to know quickly to fallback to local mode
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	conn, err := grpc.DialContext(ctx, addr, 
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, err
	}

	return &gRPCClient{
		conn:   conn,
		client: v1.NewAgentServiceClient(conn),
	}, nil
}

// ExecuteStream sends a query and returns a generic LLM StreamEvent channel
func (c *gRPCClient) ExecuteStream(ctx context.Context, query string) (<-chan llm.StreamEvent, error) {
	req := &v1.ExecuteRequest{Query: query}
	stream, err := c.client.Execute(ctx, req)
	if err != nil {
		return nil, err
	}

	out := make(chan llm.StreamEvent)

	go func() {
		defer close(out)
		defer c.conn.Close()


		for {
			resp, err := stream.Recv()
			if err != nil {
				// End of stream or error
				return
			}

			// Map gRPC response back to internal StreamEvent
			event := llm.StreamEvent{
				Content: resp.Content,
			}

			switch resp.Type {
			case v1.ExecuteResponse_TOKEN:
				event.Type = llm.EventTypeToken
			case v1.ExecuteResponse_TOOL_CALL:
				event.Type = llm.EventTypeSkillCall
			case v1.ExecuteResponse_ERROR:
				event.Type = llm.EventTypeError
				event.Err = fmt.Errorf("%s", resp.Content)
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
