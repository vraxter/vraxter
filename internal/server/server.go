package server

import (
	v1 "github.com/patagonicrune/vraxter/api/v1"
	"github.com/patagonicrune/vraxter/internal/config"
	"github.com/patagonicrune/vraxter/internal/core"
	"github.com/patagonicrune/vraxter/internal/services"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"time"
)

// BuildServer constructs the gRPC server with the given core Engine, Managers, and Daemon API Key
func BuildServer(engine *core.Engine, pm *services.ProviderManager, mm *services.ModelManager, sm *services.SkillService, cfg *config.Config, daemonKey string) *grpc.Server {

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
		grpc.UnaryInterceptor(UnaryAuthInterceptor(daemonKey)),
		grpc.StreamInterceptor(StreamAuthInterceptor(daemonKey)),
	)

	// Register the Core Agent Service
	handler := NewAgentHandler(engine, pm, mm, sm, cfg)
	v1.RegisterAgentServiceServer(s, handler)

	return s
}
