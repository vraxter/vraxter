// Copyright (c) 2026 PatagonicRune. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact PatagonicRune.

package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/vraxter/vraxter/internal/db"
	"github.com/vraxter/vraxter/pkg/types"
)

// LocalLLMService manages the lifecycle of local LLM providers (currently focused on Ollama)
type LocalLLMService struct {
	repo *db.ModelRepository
}

func NewLocalLLMService(repo *db.ModelRepository) *LocalLLMService {
	return &LocalLLMService{repo: repo}
}

// OllamaStatus represents the current state of the local Ollama installation
type OllamaStatus struct {
	Installed  bool     `json:"installed"`
	Running    bool     `json:"running"`
	Version    string   `json:"version,omitempty"`
	Models     []string `json:"models"`
	ListenAddr string   `json:"listen_addr"`
}

// GetOllamaStatus probes the system for Ollama
func (s *LocalLLMService) GetOllamaStatus(ctx context.Context) (OllamaStatus, error) {
	status := OllamaStatus{
		ListenAddr: "http://localhost:11434",
	}

	// 1. Check if binary exists
	_, err := exec.LookPath("ollama")
	if err == nil {
		status.Installed = true
	}

	// 2. Check if server is responding
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get(status.ListenAddr + "/api/tags")
	if err == nil {
		status.Running = true
		defer resp.Body.Close()

		var tags struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tags); err == nil {
			for _, m := range tags.Models {
				status.Models = append(status.Models, m.Name)
			}
		}
		
		// Try to get version
		vResp, vErr := client.Get(status.ListenAddr + "/api/version")
		if vErr == nil {
			defer vResp.Body.Close()
			var v struct {
				Version string `json:"version"`
			}
			json.NewDecoder(vResp.Body).Decode(&v)
			status.Version = v.Version
		}
	}

	return status, nil
}

// SyncOllamaModels detects all local models and registers them in Vraxter
func (s *LocalLLMService) SyncOllamaModels(ctx context.Context) (int, error) {
	status, err := s.GetOllamaStatus(ctx)
	if err != nil {
		return 0, err
	}

	if !status.Running {
		return 0, fmt.Errorf("ollama is not running")
	}

	newCount := 0
	for _, name := range status.Models {
		added, err := s.AutoRegisterLocalModel(name)
		if err == nil && added {
			newCount++
		}
	}

	return newCount, nil
}

// PullOllamaModel triggers a background pull and streams progress via callback
func (s *LocalLLMService) PullOllamaModel(ctx context.Context, name string, onProgress func(percent float64, status string)) error {
	// We use the Ollama API directly for better progress tracking
	url := "http://localhost:11434/api/pull"
	payload := map[string]string{"name": name}
	data, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(data)))
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to communicate with Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama pull failed with status: %d", resp.StatusCode)
	}

	decoder := json.NewDecoder(resp.Body)
	for {
		var chunk struct {
			Status    string `json:"status"`
			Digest    string `json:"digest"`
			Total     int64  `json:"total"`
			Completed int64  `json:"completed"`
			Error     string `json:"error"`
		}
		if err := decoder.Decode(&chunk); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		if chunk.Error != "" {
			return fmt.Errorf("ollama error: %s", chunk.Error)
		}

		if onProgress != nil {
			percent := 0.0
			if chunk.Total > 0 {
				percent = float64(chunk.Completed) / float64(chunk.Total) * 100
			}
			onProgress(percent, chunk.Status)
		}
	}

	// Auto-register after success
	_, err = s.AutoRegisterLocalModel(name)
	return err
}

// AutoRegisterLocalModel enters a pulled Ollama model into Vraxter's DB
func (s *LocalLLMService) AutoRegisterLocalModel(name string) (bool, error) {
	cfg := types.ModelConfig{
		ID:       uuid.New().String(),
		Alias:    fmt.Sprintf("Local: %s", name),
		Provider: "ollama",
		Model:    name,
		BaseURL:  "http://localhost:11434",
		Priority: 10, // Higher priority by default for local models
		IsActive: true,
	}

	// Check if already exists to avoid duplicates
	existing, _ := s.repo.GetAllModels()
	for _, m := range existing {
		if m.Provider == "ollama" && m.Model == name {
			return false, nil // Already registered
		}
	}

	return true, s.repo.UpsertModel(cfg)
}

// StartOllamaServer attempts to launch the ollama daemon if installed but not running
func (s *LocalLLMService) StartOllamaServer() error {
	_, err := exec.LookPath("ollama")
	if err != nil {
		return fmt.Errorf("ollama binary not found in PATH. Please install it from https://ollama.com")
	}

	cmd := exec.Command("ollama", "serve")
	// Start in background
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ollama server: %w", err)
	}

	// Wait a bit for it to bind
	time.Sleep(2 * time.Second)
	return nil
}

// InstallOllamaHelp returns instructions for the user OS
func (s *LocalLLMService) InstallOllamaHelp() string {
	// Simple helpful strings
	return "Ollama is not installed. Please visit https://ollama.com to download and install Vraxter's preferred local engine."
}
