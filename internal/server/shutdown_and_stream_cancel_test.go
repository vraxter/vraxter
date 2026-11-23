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
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/client"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/skills"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func TestAgentHandler_SystemShutdownSignalsGracefulChannel(t *testing.T) {
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	cryptoSvc, err := security.NewCryptoService(t.TempDir() + "/.masterkey")
	if err != nil {
		t.Fatalf("NewCryptoService: %v", err)
	}

	reg := skills.NewRegistry()
	runner := skills.NewRunner()
	defer runner.Close(context.Background())

	eng, err := core.NewEngine(store, cryptoSvc, reg, runner, nil, nil, false, t.TempDir(), false, "airgap", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	cfg := &config.Config{AppDir: t.TempDir(), IsolationMode: "airgap", PrivacyPolicy: "strict_local"}
	masterKey := "shutdown-root-key-123"
	shutdownCh := make(chan struct{}, 1)

	srv := BuildServerWithShutdown(eng, nil, nil, nil, cfg, masterKey, shutdownCh)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	defer func() {
		srv.Stop()
		_ = lis.Close()
	}()

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()

	cli := v1.NewAgentServiceClient(conn)
	ctx := metadata.AppendToOutgoingContext(context.Background(), "authorization", masterKey)
	stream, err := cli.Execute(ctx, &v1.ExecuteRequest{Query: "SYSTEM_SHUTDOWN"})
	if err != nil {
		t.Fatalf("Execute SYSTEM_SHUTDOWN failed: %v", err)
	}

	resp, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv failed: %v", err)
	}
	if resp.Type != v1.ExecuteResponse_DONE || !strings.Contains(strings.ToLower(resp.Content), "shutting down") {
		t.Fatalf("unexpected shutdown response: %+v", resp)
	}

	// Drain EOF cleanly
	_, err = stream.Recv()
	if err != nil && err != io.EOF {
		t.Fatalf("unexpected trailing stream error: %v", err)
	}

	// Verify shutdown channel was signaled cleanly (without calling os.Exit(0)!)
	select {
	case <-shutdownCh:
		// Graceful shutdown notification received!
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for graceful shutdown signal on shutdownCh")
	}
}

func TestGRPCClient_StreamCancelNoGoroutineLeak(t *testing.T) {
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	cryptoSvc, err := security.NewCryptoService(t.TempDir() + "/.masterkey")
	if err != nil {
		t.Fatalf("NewCryptoService: %v", err)
	}

	reg := skills.NewRegistry()
	runner := skills.NewRunner()
	defer runner.Close(context.Background())

	eng, err := core.NewEngine(store, cryptoSvc, reg, runner, nil, nil, false, t.TempDir(), false, "airgap", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	cfg := &config.Config{AppDir: t.TempDir(), IsolationMode: "airgap", PrivacyPolicy: "strict_local"}
	masterKey := "stream-cancel-root-key"

	srv := BuildServer(eng, nil, nil, nil, cfg, masterKey)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	go func() { _ = srv.Serve(lis) }()
	defer func() {
		srv.Stop()
		_ = lis.Close()
	}()

	gClient, err := client.NewGRPCClient(lis.Addr().String(), masterKey)
	if err != nil {
		t.Fatalf("NewGRPCClient: %v", err)
	}
	defer gClient.Close()

	// Verify GetConfig works over real gRPC client
	cfgResp, err := gClient.GetConfig(context.Background())
	if err != nil {
		t.Fatalf("GetConfig failed: %v", err)
	}
	if cfgResp.IsolationMode != "airgap" {
		t.Fatalf("expected IsolationMode='airgap', got %q", cfgResp.IsolationMode)
	}

	runtime.GC()
	time.Sleep(30 * time.Millisecond)
	baseline := runtime.NumGoroutine()

	// Open and cancel multiple SubscribeEvents streams while broadcasting events without draining them
	for i := 0; i < 6; i++ {
		subCtx, cancel := context.WithCancel(context.Background())
		evCh, err := gClient.SubscribeEvents(subCtx, "client-cancel-test")
		if err != nil {
			cancel()
			t.Fatalf("SubscribeEvents %d failed: %v", i, err)
		}
		eng.JobManager().Broadcast(core.SystemEvent{
			Type:      core.EventSystemAlert,
			Title:     "Alert",
			Payload:   "test",
			Timestamp: time.Now(),
		})
		time.Sleep(15 * time.Millisecond)
		cancel() // Cancel WITHOUT reading evCh!

		select {
		case _, ok := <-evCh:
			if ok {
				select {
				case <-evCh:
				case <-time.After(2 * time.Second):
					t.Fatalf("SubscribeEvents channel did not close after ctx cancel on iter %d", i)
				}
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("SubscribeEvents goroutine hung on iter %d", i)
		}
	}

	var after int
	for i := 0; i < 20; i++ {
		runtime.GC()
		time.Sleep(25 * time.Millisecond)
		after = runtime.NumGoroutine()
		if after <= baseline+6 {
			break
		}
	}
	if after > baseline+6 {
		t.Fatalf("goroutine leak after canceling gRPC streams: baseline=%d, after=%d", baseline, after)
	}
}
