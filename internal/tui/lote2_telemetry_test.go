// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/vraxter/vraxter/api/v1"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/security"
)

func TestTUI_TelemetryReflectsRealIsolationMode(t *testing.T) {
	cases := []struct {
		mode              string
		wantJobBadge      string
		forbidJobBadge    string
		wantAirgapContain []string
		forbidAirgap      []string
	}{
		{
			mode:              "airgap",
			wantJobBadge:      "STRICT_LOCAL VERIFIED",
			forbidJobBadge:    "HYBRID MODE",
			wantAirgapContain: []string{"AIRGAP", "DEACTIVATED", "Zero-Egress"},
			forbidAirgap:      []string{"ENABLED"},
		},
		{
			mode:              "local_lan",
			wantJobBadge:      "LOCAL_LAN VERIFIED",
			forbidJobBadge:    "STRICT_LOCAL VERIFIED",
			wantAirgapContain: []string{"LOCAL_LAN", "DEACTIVATED", "LAN-Only"},
			forbidAirgap:      []string{"Loopback (127.0.0.1, ::1) only"},
		},
		{
			mode:              "hybrid",
			wantJobBadge:      "HYBRID MODE",
			forbidJobBadge:    "STRICT_LOCAL VERIFIED",
			wantAirgapContain: []string{"HYBRID", "ENABLED", "WAN Egress Permitted"},
			forbidAirgap:      []string{"DEACTIVATED", "Zero-Egress"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			m := &Model{
				width:                110,
				height:               36,
				ActiveCardHistoryIdx: -1,
				IsolationMode:        tc.mode,
				WhitelistedIPs:       []string{"10.10.0.0/16"},
			}

			// 1. Check JOB_COMPLETED notification accuracy
			updatedM, _ := m.Update(SystemEventMsgWrapper{
				Event: &v1.SystemEvent{
					Type:  v1.SystemEvent_JOB_COMPLETED,
					Title: "Telemetry Job",
				},
			})
			m = updatedM.(*Model)
			if len(m.History) == 0 {
				t.Fatal("expected JOB_COMPLETED block in history")
			}
			jobBlock := stripANSI(m.History[len(m.History)-1].Content)
			if !strings.Contains(jobBlock, tc.wantJobBadge) {
				t.Errorf("[%s] expected JOB_COMPLETED badge %q, got: %s", tc.mode, tc.wantJobBadge, jobBlock)
			}
			if strings.Contains(jobBlock, tc.forbidJobBadge) {
				t.Errorf("[%s] JOB_COMPLETED falsely showed %q: %s", tc.mode, tc.forbidJobBadge, jobBlock)
			}

			// 2. Check /airgap telemetry output accuracy
			_ = m.handleSlashCommand("/airgap")
			airgapBlock := stripANSI(m.History[len(m.History)-1].Content)
			for _, want := range tc.wantAirgapContain {
				if !strings.Contains(airgapBlock, want) {
					t.Errorf("[%s] expected /airgap telemetry to contain %q, got:\n%s", tc.mode, want, airgapBlock)
				}
			}
			for _, forbid := range tc.forbidAirgap {
				if strings.Contains(airgapBlock, forbid) {
					t.Errorf("[%s] /airgap telemetry must NOT contain %q, got:\n%s", tc.mode, forbid, airgapBlock)
				}
			}
		})
	}
}

func TestTUI_ScopedProviderManagerRespectsIsolationMode(t *testing.T) {
	store, err := db.NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer store.Close()

	cryptoSvc, err := security.NewCryptoService(filepath.Join(t.TempDir(), ".masterkey"))
	if err != nil {
		t.Fatalf("NewCryptoService: %v", err)
	}
	pRepo := db.NewProviderRepository(store, cryptoSvc)

	for _, mode := range []string{"airgap", "local_lan"} {
		m := &Model{
			appStore:      store,
			appCrypto:     cryptoSvc,
			IsolationMode: mode,
		}
		mgr := m.newScopedProviderManager(pRepo)
		if mgr.IsolationMode() != mode {
			t.Fatalf("expected scoped ProviderManager mode %q, got %q", mode, mgr.IsolationMode())
		}
		if _, err := mgr.AddProvider(context.Background(), "CloudBlocked", "openai", "sk-123", ""); err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Fatalf("[%s] TUI wizard ProviderManager must block cloud providers, got: %v", mode, err)
		}
	}
}
