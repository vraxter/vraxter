// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package env_test

import (
	"sync"
	"testing"
	"time"

	"github.com/patagonicrune/vraxter/internal/env"
)

func TestStateManagerConcurrency(t *testing.T) {
	sm, err := env.NewStateManager(t.TempDir())
	if err != nil {
		t.Fatalf("Failed to initialize state manager: %v", err)
	}

	var wg sync.WaitGroup
	workers := 100

	// 1. Concurrent Reads
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = sm.GetState()
			_ = sm.GetActiveMode()
		}()
	}

	// 2. Concurrent Writes
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sm.MutateState(func(state *env.EnvironmentalState) {
				state.TensionLevel = id % 10
				state.MusicStyle = "testing"
			})
		}(i)
	}

	// 3. Concurrent Session Pings
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sm.RegisterSession("room-1", time.Now().UnixNano())
		}(i)
	}

	wg.Wait()

	finalState := sm.GetState()
	if finalState.MusicStyle != "testing" {
		t.Errorf("Expected MusicStyle to be 'testing', got '%s'", finalState.MusicStyle)
	}
}
