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
	"strings"
	"testing"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/services"
	"github.com/vraxter/vraxter/internal/utils"
)

func TestIsAllowedNetwork(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		url       string
		whitelist []string
		allowed   bool
	}{
		{"Airgap Localhost Allowed", "airgap", "http://localhost:11434", nil, true},
		{"Airgap Local IP Allowed", "airgap", "http://127.0.0.1:8080", nil, true},
		{"Airgap Private LAN Allowed", "airgap", "http://192.168.1.100", nil, true},
		{"Airgap Private Class A Allowed", "airgap", "http://10.0.0.5", nil, true},
		{"Airgap Public Cloud Blocked (Zero DNS)", "airgap", "https://api.openai.com", nil, false},
		{"Airgap Public Search Blocked (Zero DNS)", "airgap", "https://google.com", nil, false},
		{"Airgap Public IP Blocked", "airgap", "http://8.8.8.8", nil, false},
		{"Airgap Whitelisted Public IP Allowed", "airgap", "http://8.8.8.8", []string{"8.8.8.8"}, true},
		{"Airgap Whitelisted Hostname Allowed", "airgap", "https://api.mycompany.com", []string{"api.mycompany.com"}, true},
		{"Local LAN Private IP Allowed", "local_lan", "http://192.168.1.100", nil, true},
		{"Local LAN Private Class A Allowed", "local_lan", "http://10.0.0.5", nil, true},
		{"Local LAN Public IP Blocked", "local_lan", "http://8.8.8.8", nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			allowed, _ := utils.IsAllowedNetworkWithMode(tc.url, tc.mode, tc.whitelist)
			if allowed != tc.allowed {
				t.Errorf("expected allowed=%v for url=%s in mode=%s, got %v", tc.allowed, tc.url, tc.mode, allowed)
			}
		})
	}
}

func TestProviderManager_StrictLocal_AddProvider(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewProviderRepository(store, cs)

	// Init manager with strict_local and no whitelist
	pm := services.NewProviderManager(repo, "strict_local", nil)

	// 1. Adding a known cloud provider should fail
	_, err := pm.AddProvider(context.Background(), "OpenAI", "openai", "sk-123", "https://api.openai.com")
	if err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Errorf("expected cloud provider to be blocked, got: %v", err)
	}

	// 2. Adding a custom provider with public URL should fail
	_, err = pm.AddProvider(context.Background(), "Custom Pub", "custom", "none", "http://8.8.8.8")
	if err == nil || (!strings.Contains(err.Error(), "unapproved public IP") && !strings.Contains(err.Error(), "strict_local policy blocks internet access")) {
		t.Errorf("expected public IP to be blocked, got: %v", err)
	}

	// 3. Adding custom provider with local URL should succeed (if healthcheck passed, but it will fail healthcheck, so we just check the privacy error is avoided)
	// We'll use a dummy invalid URL just to see if it passes the strict_local check and fails at the health check instead.
	// Actually, health check is skipped or fails later. Let's verify the error is NOT a privacy policy error.
	_, err = pm.AddProvider(context.Background(), "Custom Loc", "custom", "none", "http://127.0.0.1:9999")
	if err != nil && strings.Contains(err.Error(), "network privacy policy") {
		t.Errorf("expected local IP to pass strict_local check, got: %v", err)
	}
}

func TestProviderManager_StrictLocal_UpdateProviderBypass(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewProviderRepository(store, cs)

	pm := services.NewProviderManager(repo, "strict_local", nil)

	// Directly insert a valid local provider into repo to bypass AddProvider healthcheck requirement for test
	id := "local-p"
	repo.Create(&db.Provider{
		ID: id, Name: "Local", Type: "custom", BaseURL: "http://127.0.0.1:11434", IsActive: true,
	})

	// Attempt to update it to a public IP
	pubURL := "http://8.8.8.8"
	err := pm.UpdateProvider(context.Background(), id, nil, nil, &pubURL)
	if err == nil || (!strings.Contains(err.Error(), "unapproved public IP") && !strings.Contains(err.Error(), "strict_local policy blocks internet access")) {
		t.Errorf("expected UpdateProvider to block public IP bypass, got: %v", err)
	}
}
