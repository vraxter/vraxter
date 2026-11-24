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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/audit"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/core"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
	"github.com/vraxter/vraxter/internal/skills"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
)

func issueNamedMTLSClientCert(t *testing.T, pkiDir, commonName string) (certPath, keyPath string) {
	t.Helper()
	caCertPEM, err := os.ReadFile(filepath.Join(pkiDir, "ca.crt"))
	if err != nil {
		t.Fatalf("read ca.crt: %v", err)
	}
	caKeyPEM, err := os.ReadFile(filepath.Join(pkiDir, "ca.key"))
	if err != nil {
		t.Fatalf("read ca.key: %v", err)
	}
	cb, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(cb.Bytes)
	if err != nil {
		t.Fatalf("parse ca cert: %v", err)
	}
	kb, _ := pem.Decode(caKeyPEM)
	caKey, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		t.Fatalf("parse ca key: %v", err)
	}

	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName, Organization: []string{"Vraxter Test Ops"}},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		DNSNames:     []string{commonName + ".internal.test"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("sign client cert: %v", err)
	}

	certPath = filepath.Join(pkiDir, commonName+".crt")
	keyPath = filepath.Join(pkiDir, commonName+".key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		t.Fatalf("write client cert: %v", err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(clientKey)
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		t.Fatalf("write client key: %v", err)
	}
	return certPath, keyPath
}

func TestMTLSAndAPIKeyPrincipal_RecordedInAuditLedgerIgnoringSpoofedSourceUser(t *testing.T) {
	appDir := t.TempDir()
	store, err := db.NewStore(filepath.Join(appDir, "vraxter.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	cryptoSvc, err := security.NewCryptoService(filepath.Join(appDir, ".masterkey"))
	if err != nil {
		t.Fatalf("NewCryptoService: %v", err)
	}

	reg := skills.NewRegistry()
	runner := skills.NewRunner()
	defer runner.Close(context.Background())

	engine, err := core.NewEngine(store, cryptoSvc, reg, runner, nil, false, appDir, false, "airgap", nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	ledger := engine.AuditLedger()
	if ledger == nil {
		t.Fatal("expected initialized audit ledger on engine")
	}

	apiKeyRepo := db.NewAPIKeyRepository(store)
	SetAPIKeyRepo(apiKeyRepo)
	t.Cleanup(func() { SetAPIKeyRepo(nil) })

	scopedToken := "vrx_scoped_test_token_123456"
	tokenSum := sha256.Sum256([]byte(scopedToken))
	if err := apiKeyRepo.Create(&db.APIKey{
		ID:      "key-ops-007",
		Name:    "soc-console",
		KeyHash: hex.EncodeToString(tokenSum[:]),
		Scopes:  "chat:write",
	}); err != nil {
		t.Fatalf("Create APIKey: %v", err)
	}

	// Provision local mTLS 1.3 PKI and issue a named operator client certificate.
	pkiPaths, err := security.EnsureLocalPKI(appDir, []string{"127.0.0.1:0"})
	if err != nil {
		t.Fatalf("EnsureLocalPKI: %v", err)
	}
	clientCertPath, clientKeyPath := issueNamedMTLSClientCert(t, filepath.Dir(pkiPaths.CAPath), "ops-commander-alpha")

	srvTLS, err := security.LoadServerTLSConfig(pkiPaths.CAPath, pkiPaths.ServerCertPath, pkiPaths.ServerKeyPath)
	if err != nil {
		t.Fatalf("LoadServerTLSConfig: %v", err)
	}
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer lis.Close()

	rootKey := "root-secret-key-xyz"
	cfg := &config.Config{AppDir: appDir, IsolationMode: "airgap"}
	grpcSrv := BuildServer(engine, nil, nil, nil, cfg, rootKey, grpc.Creds(credentials.NewTLS(srvTLS)))
	go func() { _ = grpcSrv.Serve(lis) }()
	defer grpcSrv.Stop()

	clientTLS, err := security.LoadClientTLSConfig(pkiPaths.CAPath, clientCertPath, clientKeyPath, "127.0.0.1")
	if err != nil {
		t.Fatalf("LoadClientTLSConfig: %v", err)
	}
	clientTLS.MinVersion = tls.VersionTLS13

	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatalf("grpc.NewClient: %v", err)
	}
	defer conn.Close()
	cli := v1.NewAgentServiceClient(conn)

	// 1. mTLS + Scoped API Key with SPOOFED SourceUser="fake-admin" -> ledger must record "mtls:ops-commander-alpha|api_key:key-ops-007"
	ctx1 := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+scopedToken))
	stream1, err := cli.Execute(ctx1, &v1.ExecuteRequest{
		Query:          "status check",
		ConversationId: "sess-mtls-scoped",
		SourceUser:     "fake-unverified-admin",
	})
	if err != nil {
		t.Fatalf("Execute (mtls+api_key) failed: %v", err)
	}
	for {
		if _, rErr := stream1.Recv(); rErr != nil {
			break
		}
	}

	// 2. mTLS + Root Key with SPOOFED SourceUser="fake-root" -> ledger must record "mtls:ops-commander-alpha"
	ctx2 := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+rootKey))
	stream2, err := cli.Execute(ctx2, &v1.ExecuteRequest{
		Query:          "status check 2",
		ConversationId: "sess-mtls-root",
		SourceUser:     "fake-unverified-root",
	})
	if err != nil {
		t.Fatalf("Execute (mtls+root) failed: %v", err)
	}
	for {
		if _, rErr := stream2.Recv(); rErr != nil {
			break
		}
	}

	// 3. Denied scope under mTLS + Scoped API Key (attempting $ command without host:exec) must also log verified actor.
	stream3, err := cli.Execute(ctx1, &v1.ExecuteRequest{
		Query:          "$ping 127.0.0.1",
		ConversationId: "sess-mtls-denied",
		SourceUser:     "spoofed-attacker",
	})
	if err == nil {
		for {
			if _, rErr := stream3.Recv(); rErr != nil {
				break
			}
		}
	}

	entries, err := ledger.ListEntries(audit.ExportFilter{})
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}

	foundScopedActor := false
	foundRootMTLSActor := false
	foundDeniedScopedActor := false
	for _, e := range entries {
		if strings.Contains(e.Actor, "fake-unverified") || strings.Contains(e.Actor, "spoofed-attacker") || e.Actor == "user" {
			t.Fatalf("SECURITY VIOLATION: audit_ledger recorded unverified/generic actor %q at seq=%d (%s)", e.Actor, e.Seq, e.EventType)
		}
		if e.SessionID == "sess-mtls-scoped" && e.Actor == "mtls:ops-commander-alpha|api_key:key-ops-007" {
			foundScopedActor = true
		}
		if e.SessionID == "sess-mtls-root" && e.Actor == "mtls:ops-commander-alpha" {
			foundRootMTLSActor = true
		}
		if e.SessionID == "sess-mtls-denied" && e.EventType == audit.EventPolicyEvaluated && e.Actor == "mtls:ops-commander-alpha|api_key:key-ops-007" {
			foundDeniedScopedActor = true
		}
	}

	if !foundScopedActor {
		t.Fatalf("expected verified actor 'mtls:ops-commander-alpha|api_key:key-ops-007' in audit_ledger; entries=%+v", entries)
	}
	if !foundRootMTLSActor {
		t.Fatalf("expected verified actor 'mtls:ops-commander-alpha' in audit_ledger; entries=%+v", entries)
	}
	if !foundDeniedScopedActor {
		t.Fatalf("expected verified actor on denied scope event in audit_ledger; entries=%+v", entries)
	}
	if _, err := ledger.VerifyChain(); err != nil {
		t.Fatalf("audit ledger chain verification failed: %v", err)
	}
}
