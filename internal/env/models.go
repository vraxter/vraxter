// Copyright (c) 2026 Vraxter. All rights reserved.
// 
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package env

import "time"

// EnvironmentalState defines the abstract, unified state that Vraxter's core executes.
type EnvironmentalState struct {
	TensionLevel        int      `json:"tension_level"`        // Scale 0-10 (Silence/Calm -> High Tension/Combat)
	ConversationPattern string   `json:"conversation_pattern"` // "paused", "dynamic", "shouting", "monologue"
	MusicStyle          string   `json:"music_style"`          // Genre/Mood modifier for Spotify/YT Music skill
	TriggerKeywords     []string `json:"trigger_keywords"`     // Special immediate sound effects (.mp3) to trigger
	EyeColor            string   `json:"eye_color"`            // Hex color code for the Client projector eye (e.g., "#FF0000")
	EyeDynamics         string   `json:"eye_dynamics"`         // Animation state: "pulse", "static", "max_alert", "breathe"
}

// ModeConfig represents a hot-swappable behavioral template that Vraxter can read or write dynamically.
type ModeConfig struct {
	ModeName   string             `json:"mode_name"`   // e.g., "D&D 5e Table", "Deep Work Session"
	CreatedAt  time.Time          `json:"created_at"`
	Directives map[string]string  `json:"directives"`  // Prompt injection overrides for the LLM translation matrix
	BaseState  EnvironmentalState `json:"base_state"`  // Baseline environmental state upon activation
}
