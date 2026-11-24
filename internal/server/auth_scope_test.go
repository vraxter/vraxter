// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"strings"
	"testing"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/skills"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func setupRealGRPCServerForScopeTests(t *testing.T) (v1.AgentServiceClient, *config.Config, *db.APIKeyRepository, string) {
	t.Helper()
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })

	apiRepo := db.NewAPIKeyRepository(store)
	SetAPIKeyRepo(apiRepo)
	t.Cleanup(func() { SetAPIKeyRepo(nil) })

	cryptoSvc, err := security.NewCryptoService(t.TempDir() + "/.masterkey")
	if err != nil {
		t.Fatalf("NewCryptoService: %v", err)
	}

	reg := skills.NewRegistry()
	runner := skills.NewRunner()
	t.Cleanup(func() { runner.Close(context.Background()) })

	eng, err := core.NewEngine(store, cryptoSvc, reg, runner, nil, false, t.TempDir(), false, "airgap", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	cfg := &config.Config{
		AppDir:         t.TempDir(),
		IsolationMode:  "airgap",
		PrivacyPolicy:  "strict_local",
		IsAirgapLocked: false,
		HostExec:       config.HostExecConfig{Enabled: false},
	}

	masterKey := "root-master-key-xyz"
	srv := BuildServer(eng, nil, nil, nil, cfg, masterKey)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() {
		_ = srv.Serve(lis)
	}()
	t.Cleanup(func() {
		srv.Stop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return v1.NewAgentServiceClient(conn), cfg, apiRepo, masterKey
}

func registerScopedToken(t *testing.T, repo *db.APIKeyRepository, id, rawToken, scopes string) {
	t.Helper()
	h := sha256.Sum256([]byte(rawToken))
	err := repo.Create(&db.APIKey{
		ID:      id,
		Name:    id,
		KeyHash: hex.EncodeToString(h[:]),
		Scopes:  scopes,
	})
	if err != nil {
		t.Fatalf("failed to create scoped token %s: %v", id, err)
	}
}

func ctxWithToken(token string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+token)
}

func TestStreamAuthInterceptor_PropagatesScopesAndEnforcesHostExec(t *testing.T) {
	client, _, repo, _ := setupRealGRPCServerForScopeTests(t)

	registerScopedToken(t, repo, "chat-only", "vrx_chat_only", "chat:write")
	registerScopedToken(t, repo, "chat-host", "vrx_chat_host", "chat:write, host:exec")
	registerScopedToken(t, repo, "skills-read", "vrx_skills_read", "skills:read")

	// 1. Token with ONLY chat:write MUST be rejected when invoking "$" host commands over real gRPC stream
	stream1, err := client.Execute(ctxWithToken("vrx_chat_only"), &v1.ExecuteRequest{Query: "$ping 127.0.0.1"})
	if err != nil {
		t.Fatalf("Execute RPC dial failed unexpectedly: %v", err)
	}
	_, err = stream1.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for $ command without host:exec scope, got: %v", err)
	}

	// 2. Token with ONLY chat:write MUST be rejected when sending !APPROVE_HOST_CMD
	stream2, err := client.Execute(ctxWithToken("vrx_chat_only"), &v1.ExecuteRequest{
		Query: `!APPROVE_HOST_CMD {"command":"nmap","args":["127.0.0.1"]}`,
	})
	if err != nil {
		t.Fatalf("Execute RPC dial failed unexpectedly: %v", err)
	}
	_, err = stream2.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for !APPROVE_HOST_CMD without host:exec scope, got: %v", err)
	}

	// 3. Token with ONLY chat:write MUST be rejected for SYSTEM_SHUTDOWN
	stream3, err := client.Execute(ctxWithToken("vrx_chat_only"), &v1.ExecuteRequest{Query: "SYSTEM_SHUTDOWN"})
	if err != nil {
		t.Fatalf("Execute RPC dial failed unexpectedly: %v", err)
	}
	_, err = stream3.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for SYSTEM_SHUTDOWN without root '*' scope, got: %v", err)
	}

	// 4. Token with ONLY skills:read MUST be rejected on Execute AND SubscribeEvents
	stream4, err := client.Execute(ctxWithToken("vrx_skills_read"), &v1.ExecuteRequest{Query: "hello"})
	if err != nil {
		t.Fatalf("Execute RPC dial failed unexpectedly: %v", err)
	}
	_, err = stream4.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for Execute with skills:read scope, got: %v", err)
	}

	subStream, err := client.SubscribeEvents(ctxWithToken("vrx_skills_read"), &v1.SubscribeEventsRequest{ClientId: "c1"})
	if err != nil {
		t.Fatalf("SubscribeEvents dial failed unexpectedly: %v", err)
	}
	_, err = subStream.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied for SubscribeEvents with skills:read scope, got: %v", err)
	}

	// 5. Token WITH chat:write + host:exec passes gRPC stream scope gate, then hits HostExecGuard (disabled by default!)
	stream5, err := client.Execute(ctxWithToken("vrx_chat_host"), &v1.ExecuteRequest{Query: "$ping 127.0.0.1"})
	if err != nil {
		t.Fatalf("Execute RPC dial failed unexpectedly: %v", err)
	}
	var allContent string
	for {
		resp, recvErr := stream5.Recv()
		if recvErr == io.EOF {
			break
		}
		if recvErr != nil {
			t.Fatalf("unexpected stream error for chat:write+host:exec token: %v", recvErr)
		}
		allContent += resp.Content
	}
	if !strings.Contains(allContent, "disabled by default") {
		t.Fatalf("expected HostExecGuard disabled message after passing scope check, got: %q", allContent)
	}
}

func TestUnaryRPCs_EnforceScopesAndProtectHostExecConfig(t *testing.T) {
	client, cfg, repo, _ := setupRealGRPCServerForScopeTests(t)
	registerScopedToken(t, repo, "skills-read", "vrx_skills_read", "skills:read")
	registerScopedToken(t, repo, "config-write", "vrx_cfg_write", "config:read,config:write")

	readCtx := ctxWithToken("vrx_skills_read")

	if _, err := client.GetConfig(readCtx, &v1.GetConfigRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on GetConfig without config:read, got: %v", err)
	}
	if _, err := client.UpdateConfig(readCtx, &v1.UpdateConfigRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on UpdateConfig without config:write, got: %v", err)
	}
	if _, err := client.ConfigureProvider(readCtx, &v1.ConfigureProviderRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on ConfigureProvider without providers:write, got: %v", err)
	}
	if _, err := client.ListHistory(readCtx, &v1.ListHistoryRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on ListHistory without chat:read, got: %v", err)
	}
	if _, err := client.GetInfo(readCtx, &v1.GetInfoRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied on GetInfo without chat:read/system:read, got: %v", err)
	}

	// Authorized UpdateConfig succeeds for allowed fields while HostExec.Enabled remains immutably false
	cfgCtx := ctxWithToken("vrx_cfg_write")
	reqApproval := true
	if _, err := client.UpdateConfig(cfgCtx, &v1.UpdateConfigRequest{RequireSkillApproval: &reqApproval}); err != nil {
		t.Fatalf("expected authorized UpdateConfig to succeed, got: %v", err)
	}
	if cfg.HostExec.Enabled {
		t.Fatal("HostExec.Enabled must never become enabled via UpdateConfig RPC")
	}
}
