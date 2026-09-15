// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package env

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// StateManager orchestrates the current active environmental state across concurrent sessions
// and persists AI mode configurations natively using standard JSON templates.
type StateManager struct {
	mu           sync.RWMutex
	modesDir     string
	activeMode   ModeConfig
	currentState EnvironmentalState
	sessions     map[string]timeSession // map[room_id]session_data
}

type timeSession struct {
	RoomID   string
	LastPing int64
}

// NewStateManager creates a new StateManager and ensures the persistence directory exists.
func NewStateManager(appDir string) (*StateManager, error) {
	modesDir := filepath.Join(appDir, "modes")
	if err := os.MkdirAll(modesDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create modes directory: %w", err)
	}

	return &StateManager{
		modesDir: modesDir,
		sessions: make(map[string]timeSession),
	}, nil
}

// GetState returns a thread-safe copy of the current EnvironmentalState.
func (sm *StateManager) GetState() EnvironmentalState {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.currentState
}

// GetActiveMode returns a thread-safe copy of the active ModeConfig.
func (sm *StateManager) GetActiveMode() ModeConfig {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.activeMode
}

// MutateState allows atomic, thread-safe mutations to the current state using a callback.
func (sm *StateManager) MutateState(mutator func(state *EnvironmentalState)) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	mutator(&sm.currentState)
}

// RegisterSession logs an active room interacting with the system.
func (sm *StateManager) RegisterSession(roomID string, timestamp int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.sessions[roomID] = timeSession{
		RoomID:   roomID,
		LastPing: timestamp,
	}
}

// SaveModeConfig writes a ModeConfig to a JSON file, allowing the LLM to generate templates.
func (sm *StateManager) SaveModeConfig(config ModeConfig) error {
	// Simple slugification
	filename := fmt.Sprintf("%s.json", sanitizeFilename(config.ModeName))
	path := filepath.Join(sm.modesDir, filename)

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal mode config: %w", err)
	}

	return os.WriteFile(path, data, 0644)
}

// LoadModeConfig dynamically loads a JSON configuration and applies its BaseState.
func (sm *StateManager) LoadModeConfig(filename string) error {
	path := filepath.Join(sm.modesDir, filename)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read mode config: %w", err)
	}

	var config ModeConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Errorf("failed to unmarshal mode config: %w", err)
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()
	sm.activeMode = config
	sm.currentState = config.BaseState

	return nil
}

// ListModes scans the modes directory and returns all available hot-swappable configurations.
func (sm *StateManager) ListModes() ([]ModeConfig, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	entries, err := os.ReadDir(sm.modesDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read modes directory: %w", err)
	}

	var modes []ModeConfig
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}

		path := filepath.Join(sm.modesDir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue // Skip corrupted files
		}

		var config ModeConfig
		if err := json.Unmarshal(data, &config); err == nil {
			modes = append(modes, config)
		}
	}

	return modes, nil
}

func sanitizeFilename(name string) string {
	// Very naive slug for persistence storage
	res := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			res = append(res, c)
		} else if c == ' ' || c == '_' || c == '-' {
			res = append(res, '_')
		}
	}
	return string(res)
}
