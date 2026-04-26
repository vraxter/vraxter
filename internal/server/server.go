package server

import (
	"fmt"
	"net"

	v1 "github.com/patagonicrune/vraxter/api/v1"
	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/services"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"time"
)

// Start initiates the gRPC server for the Vraxter Daemon
func Start(engine *core.Engine, pm *services.ProviderManager, mm *services.ModelManager, ready chan bool) {
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		fmt.Printf("Failed to listen on port: %v\n", err)
		return
	}

	s := grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             2 * time.Second, // extremely permissive for local dev
			PermitWithoutStream: true,            // allow pings even on idle connections
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle: 5 * time.Minute,
			Time:              20 * time.Second,
			Timeout:           10 * time.Second,
		}),
	)

	// Register the Core Agent Service
	handler := NewAgentHandler(engine, pm, mm)
	v1.RegisterAgentServiceServer(s, handler)

	fmt.Println("🚀 Vraxter Daemon listening on :50051 (gRPC)")
	ready <- true

	if err := s.Serve(lis); err != nil {
		fmt.Printf("Fatal: gRPC server error: %v\n", err)
	}
}

