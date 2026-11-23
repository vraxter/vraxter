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
	"sync"
	"testing"

	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/internal/services"
)

func TestProviderManager_LocalLANBlocksCloudProvidersAndPublicEndpoints(t *testing.T) {
	store := newTestStore(t)
	cs := newTestCrypto(t)
	repo := db.NewProviderRepository(store, cs)

	pm := services.NewProviderManager(repo, "custom", nil, "local_lan")

	// 1. Cloud providers MUST be blocked in local_lan
	for _, cloud := range []string{"openai", "anthropic", "google"} {
		_, err := pm.AddProvider(context.Background(), "Cloud-"+cloud, cloud, "sk-123", "")
		if err == nil || !strings.Contains(err.Error(), "blocked") {
			t.Errorf("local_lan must block cloud provider %q, got: %v", cloud, err)
		}
	}

	// 2. Public WAN custom endpoints and IMDS link-local MUST be blocked in local_lan
	if _, err := pm.AddProvider(context.Background(), "WANCustom", "custom", "", "http://8.8.8.8:11434"); err == nil || !strings.Contains(err.Error(), "unapproved public IP") {
		t.Errorf("local_lan must block public WAN custom endpoint, got: %v", err)
	}
	if _, err := pm.AddProvider(context.Background(), "IMDSCustom", "custom", "", "http://169.254.169.254/latest/meta-data"); err == nil {
		t.Errorf("local_lan must block IMDS link-local endpoint")
	}

	// 3. GetSupportedProviders in local_lan MUST only return local providers (ollama, custom)
	supported := pm.GetSupportedProviders()
	for _, p := range supported {
		if p == "openai" || p == "anthropic" || p == "google" {
			t.Errorf("GetSupportedProviders in local_lan leaked cloud provider %q", p)
		}
	}

	// 4. ListProviders & DiscoverModels MUST filter out pre-existing cloud providers in DB
	_ = repo.Create(&db.Provider{ID: "p-cloud", Name: "LegacyOpenAI", Type: "openai", APIKey: "sk-1", IsActive: true})
	_ = repo.Create(&db.Provider{ID: "p-lan", Name: "LANOllama", Type: "ollama", BaseURL: "http://192.168.1.50:11434", IsActive: true})

	listed, err := pm.ListProviders()
	if err != nil {
		t.Fatalf("ListProviders failed: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != "p-lan" {
		t.Fatalf("expected ListProviders in local_lan to return only LAN provider, got: %+v", listed)
	}

	if _, err := pm.DiscoverModels(context.Background(), "p-cloud"); err == nil || !strings.Contains(err.Error(), "blocked") {
		t.Fatalf("expected DiscoverModels on cloud provider in local_lan to be blocked, got: %v", err)
	}
}

func TestToolchainManager_ConcurrentAirgapToggleSafety(t *testing.T) {
	tm := services.NewToolchainManager(t.TempDir(), true)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tm.SetAirgap((idx+j)%2 == 0)
			}
		}(i)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = tm.IsAirgap()
			}
		}()
	}
	wg.Wait()

	tm.SetAirgap(true)
	if err := tm.SetupSDK("go"); err == nil || !strings.Contains(err.Error(), "airgap mode") {
		t.Fatalf("expected SetupSDK blocked when airgap=true, got: %v", err)
	}
}
