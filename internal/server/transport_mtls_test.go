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
	"crypto/tls"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/vraxter/vraxter/internal/client"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/security"
)

func TestUnixSocketListener_StaleSocketRecoveryAndActiveDaemonProtection(t *testing.T) {
	// Use a short directory path in os.TempDir() so sun_path stays well under the 104-108 byte OS limit.
	shortDir, err := os.MkdirTemp("", "vrx-uds-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	defer os.RemoveAll(shortDir)

	sockPath := filepath.Join(shortDir, "vraxter.sock")
	endpoint := "unix://" + sockPath

	// 1. Seed a stale orphan .sock file left behind by a crashed process (no listener bound).
	if err := os.WriteFile(sockPath, []byte("orphan-socket-artifact"), 0600); err != nil {
		t.Fatalf("seed stale socket file: %v", err)
	}

	lis, credOpt, err := ListenEndpoint(endpoint, nil, false)
	if err != nil {
		t.Fatalf("ListenEndpoint over stale unix socket failed: %v", err)
	}
	defer lis.Close()
	if credOpt != nil {
		t.Fatalf("expected nil TLS ServerOption for local UDS listener")
	}

	if runtime.GOOS != "windows" {
		fi, err := os.Stat(sockPath)
		if err != nil {
			t.Fatalf("Stat unix socket: %v", err)
		}
		if fi.Mode().Perm() != 0600 {
			t.Fatalf("expected unix socket permissions 0600, got %o", fi.Mode().Perm())
		}
	}

	masterKey := "vrx_uds_master_key_0123456789abcdef"
	cfg := &config.Config{AppDir: shortDir, IsolationMode: "airgap"}
	srv := BuildServerWithShutdown(nil, nil, nil, nil, cfg, masterKey, nil, credOpt)
	defer srv.Stop()
	go func() {
		_ = srv.Serve(lis)
	}()

	// 2. Connect gRPC client over unix:// endpoint and verify live RPC succeeds.
	gClient, err := client.NewGRPCClient(endpoint, masterKey)
	if err != nil {
		t.Fatalf("NewGRPCClient(%s) failed: %v", endpoint, err)
	}
	defer gClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	info, err := gClient.GetInfo(ctx)
	if err != nil || info["status"] != "READY" {
		t.Fatalf("expected GetInfo over UDS to return status=READY, got info=%v err=%v", info, err)
	}

	// 3. Attempting to bind a second listener on the same active socket MUST fail closed
	//    without unlinking or disrupting the active daemon's socket!
	if _, _, err := ListenEndpoint(endpoint, nil, false); err == nil || !strings.Contains(err.Error(), "already running") {
		t.Fatalf("expected second ListenEndpoint on active socket to fail closed, got: %v", err)
	}

	// Verify active daemon still serves RPCs over the socket after the rejected duplicate bind.
	infoAfter, err := gClient.GetInfo(ctx)
	if err != nil || infoAfter["status"] != "READY" {
		t.Fatalf("active UDS daemon was disrupted by duplicate bind check: info=%v err=%v", infoAfter, err)
	}
}

func TestTCPListener_EnforcesMandatoryMTLS13AndRejectsUnauthenticatedClients(t *testing.T) {
	// 1. Non-loopback TCP and forced-mTLS TCP MUST fail closed when mTLS 1.3 config is missing or weak.
	for _, ep := range []string{"tcp://0.0.0.0:0", "tcp://192.168.1.100:50051"} {
		if _, _, err := ListenEndpoint(ep, nil, false); err == nil || !strings.Contains(err.Error(), "mandatory mTLS 1.3") {
			t.Fatalf("expected %s without TLS config to fail closed, got: %v", ep, err)
		}
	}
	if _, _, err := ListenEndpoint("tcp://127.0.0.1:0", nil, true); err == nil || !strings.Contains(err.Error(), "mandatory mTLS 1.3") {
		t.Fatalf("expected forced-mTLS TCP listener without TLS config to fail closed, got: %v", err)
	}

	// Weak TLS 1.2 or NoClientCert config MUST also be rejected
	weakTLS := &tls.Config{MinVersion: tls.VersionTLS12, ClientAuth: tls.RequireAndVerifyClientCert}
	if _, _, err := ListenEndpoint("tcp://0.0.0.0:0", weakTLS, true); err == nil || !strings.Contains(err.Error(), "TLS 1.3") {
		t.Fatalf("expected TLS 1.2 config to be rejected, got: %v", err)
	}
	noClientAuthTLS := &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.NoClientCert}
	if _, _, err := ListenEndpoint("tcp://0.0.0.0:0", noClientAuthTLS, true); err == nil || !strings.Contains(err.Error(), "RequireAndVerifyClientCert") {
		t.Fatalf("expected NoClientCert config to be rejected, got: %v", err)
	}

	// 2. Provision auto-generated local PKI (ECDSA P-256) and start an mTLS 1.3 gRPC server.
	pkiDir := t.TempDir()
	pki, err := security.EnsureLocalPKI(pkiDir, []string{"127.0.0.1", "localhost"})
	if err != nil {
		t.Fatalf("EnsureLocalPKI: %v", err)
	}

	serverTLS, err := security.LoadServerTLSConfig(pki.CAPath, pki.ServerCertPath, pki.ServerKeyPath)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}

	lis, credOpt, err := ListenEndpoint("tcp://127.0.0.1:0", serverTLS, true)
	if err != nil {
		t.Fatalf("ListenEndpoint with valid mTLS 1.3 failed: %v", err)
	}
	defer lis.Close()

	masterKey := "vrx_mtls_master_key_0123456789abcdef"
	cfg := &config.Config{AppDir: pkiDir, IsolationMode: "airgap"}
	srv := BuildServerWithShutdown(nil, nil, nil, nil, cfg, masterKey, nil, credOpt)
	defer srv.Stop()
	go func() {
		_ = srv.Serve(lis)
	}()

	tcpEndpoint := "tcp://" + lis.Addr().String()

	// 3. Client without mTLS client certificate (insecure plaintext) MUST be rejected.
	if plainClient, err := client.NewGRPCClient(tcpEndpoint, masterKey); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		_, rpcErr := plainClient.GetInfo(ctx)
		cancel()
		_ = plainClient.Close()
		if rpcErr == nil {
			t.Fatal("expected plaintext client against mTLS 1.3 server to fail")
		}
	}

	// 4. Client presenting a certificate signed by an untrusted/rogue CA MUST be rejected.
	roguePKI, err := security.EnsureLocalPKI(t.TempDir(), []string{"127.0.0.1"})
	if err != nil {
		t.Fatalf("EnsureLocalPKI(rogue): %v", err)
	}
	if rogueClient, err := client.NewGRPCClientWithTLS(tcpEndpoint, masterKey, &client.ClientTLSOptions{
		CAPath:   pki.CAPath,              // trusts server CA
		CertPath: roguePKI.ClientCertPath, // untrusted client cert
		KeyPath:  roguePKI.ClientKeyPath,
	}); err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		_, rpcErr := rogueClient.GetInfo(ctx)
		cancel()
		_ = rogueClient.Close()
		if rpcErr == nil {
			t.Fatal("expected rogue client certificate to be rejected by mTLS 1.3 server")
		}
	}

	// 5. Client presenting valid mTLS 1.3 certificate signed by local CA MUST succeed.
	validClient, err := client.NewGRPCClientWithTLS(tcpEndpoint, masterKey, &client.ClientTLSOptions{
		CAPath:   pki.CAPath,
		CertPath: pki.ClientCertPath,
		KeyPath:  pki.ClientKeyPath,
	})
	if err != nil {
		t.Fatalf("NewGRPCClientWithTLS(valid) failed: %v", err)
	}
	defer validClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	info, err := validClient.GetInfo(ctx)
	if err != nil || info["status"] != "READY" {
		t.Fatalf("expected valid mTLS 1.3 client GetInfo to succeed, got info=%v err=%v", info, err)
	}

	// 6. Client attempting to dial a non-loopback remote TCP address without mTLS credentials MUST fail closed.
	if _, err := client.NewGRPCClient("tcp://10.20.30.40:50051", masterKey); err == nil || !strings.Contains(err.Error(), "mTLS 1.3 credentials") {
		t.Fatalf("expected client to refuse non-loopback TCP endpoint without mTLS credentials, got: %v", err)
	}
}
