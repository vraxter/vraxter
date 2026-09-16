// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package server

import (
	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/services"
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
