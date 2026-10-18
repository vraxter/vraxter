// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tests

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"syscall"
	"testing"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/config"
	"github.com/vraxter/vraxter/internal/server"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/utils"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestAirgapDialer_BlocksPublicIP(t *testing.T) {
	dialer := utils.NewAirgapDialer("airgap", nil)

	// Dialing a public IP must be immediately rejected with syscall.EPERM
	ctx := context.Background()
	_, err := dialer(ctx, "tcp", "8.8.8.8:53")
	if err == nil {
		t.Fatalf("expected dialer to block public IP 8.8.8.8, but connection succeeded")
	}

	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("expected syscall.EPERM error from airgap dialer, got: %v", err)
	}
}

func TestAirgapDialer_AllowsPrivateLAN(t *testing.T) {
	dialer := utils.NewAirgapDialer("airgap", nil)

	ctx := context.Background()
	// Dialing a private LAN IP (e.g., Ollama server on another machine) must NOT return syscall.EPERM.
	// Since no server is actually running on 192.168.1.250:11434 in unit test environment,
	// it should fail at the OS network layer (e.g. timeout / unreachable / connection refused),
	// but it must NOT be rejected by the airgap perimeter interceptor (syscall.EPERM).
	_, err := dialer(ctx, "tcp", "192.168.1.250:11434")
	if errors.Is(err, syscall.EPERM) {
		t.Fatalf("expected dialer to allow private LAN dial to 192.168.1.250:11434, but was blocked with EPERM")
	}
}

func TestAirgapDialer_BlocksExternalFQDNWithoutDNS(t *testing.T) {
	dialer := utils.NewAirgapDialer("airgap", nil)

	ctx := context.Background()
	// Dialing an external FQDN must be rejected in-memory before DNS lookup
	_, err := dialer(ctx, "tcp", "api.openai.com:443")
	if err == nil {
		t.Fatalf("expected dialer to block external FQDN in airgap mode, but connection succeeded")
	}

	if !errors.Is(err, syscall.EPERM) {
		t.Fatalf("expected syscall.EPERM error from airgap dialer, got: %v", err)
	}
}

func TestAirgapTransport_BlocksHTTPClientEgress(t *testing.T) {
	transport := utils.NewAirgapTransport("airgap", nil)
	client := &http.Client{Transport: transport}

	_, err := client.Get("http://8.8.8.8/test")
	if err == nil {
		t.Fatalf("expected AirgapTransport to block HTTP request to 8.8.8.8, got nil")
	}

	if !errors.Is(err, syscall.EPERM) && !strings.Contains(err.Error(), "operation not permitted") {
		t.Fatalf("expected EPERM (operation not permitted), got: %v", err)
	}
}

func TestAirgap_AntiDowngradeLockout(t *testing.T) {
	appCfg := &config.Config{
		IsolationMode:  "airgap",
		IsAirgapLocked: true,
		PrivacyPolicy:  "strict_local",
	}

	h := server.NewAgentHandler(nil, nil, nil, nil, appCfg)

	// Attempting to downgrade isolation_mode when locked must fail with PermissionDenied
	hybridMode := "hybrid"
	_, err := h.UpdateConfig(context.Background(), &v1.UpdateConfigRequest{
		IsolationMode: &hybridMode,
	})
	if err == nil {
		t.Fatalf("expected error when downgrading locked airgap, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied gRPC code, got: %v (code: %v)", err, st.Code())
	}

	// Attempting to downgrade privacy_policy when locked must fail with PermissionDenied
	customPrivacy := "custom"
	_, err = h.UpdateConfig(context.Background(), &v1.UpdateConfigRequest{
		PrivacyPolicy: &customPrivacy,
	})
	if err == nil {
		t.Fatalf("expected error when downgrading locked privacy_policy, got nil")
	}

	st, ok = status.FromError(err)
	if !ok || st.Code() != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied gRPC code, got: %v (code: %v)", err, st.Code())
	}
}

func TestAirgap_RuntimeDowngradePrevention(t *testing.T) {
	// Not locked by flag, but running currently in airgap mode
	appCfg := &config.Config{
		IsolationMode:  "airgap",
		IsAirgapLocked: false,
		PrivacyPolicy:  "strict_local",
	}

	h := server.NewAgentHandler(nil, nil, nil, nil, appCfg)

	// Runtime downgrade from active airgap to hybrid requires daemon restart
	hybridMode := "hybrid"
	_, err := h.UpdateConfig(context.Background(), &v1.UpdateConfigRequest{
		IsolationMode: &hybridMode,
	})
	if err == nil {
		t.Fatalf("expected runtime downgrade from airgap to be blocked, got nil")
	}

	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.PermissionDenied {
		t.Fatalf("expected PermissionDenied gRPC code for runtime downgrade, got: %v", err)
	}
}

func TestToolchain_BlocksSDKDownloadInAirgap(t *testing.T) {
	sdkDir := t.TempDir()
	tm := services.NewToolchainManager(sdkDir, true) // isAirgap = true

	err := tm.SetupSDK("go")
	if err == nil {
		t.Fatalf("expected SetupSDK to be blocked in airgap mode, but got nil")
	}

	if !strings.Contains(err.Error(), "Vraxter is running in airgap mode") {
		t.Fatalf("expected airgap block message, got: %v", err)
	}
}

