package server

import (
	"fmt"
	"net"

	v1 "github.com/patagonicrune/vraxter/api/v1"
	"github.com/patagonicrune/vraxter/internal/core"
	"google.golang.org/grpc"
)

// Start initiates the gRPC server for the Vraxter Daemon
func Start(engine *core.Engine, ready chan bool) {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		fmt.Printf("Failed to listen on port: %v\n", err)
		return
	}

	s := grpc.NewServer()

	// Register the Core Agent Service
	handler := NewAgentHandler(engine)
	v1.RegisterAgentServiceServer(s, handler)

	fmt.Println("🚀 Vraxter Daemon listening on :50051 (gRPC)")
	ready <- true

	if err := s.Serve(lis); err != nil {
		fmt.Printf("Fatal: gRPC server error: %v\n", err)
	}
}

