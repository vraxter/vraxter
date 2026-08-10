package env

import (
	"log/slog"
	"sync"
)

// ExecutionPipeline orchestrates side-effects when the environmental state changes.
type ExecutionPipeline struct {
	mu             sync.Mutex
	lastMusicStyle string
}

// NewExecutionPipeline initializes the execution wrapper.
func NewExecutionPipeline() *ExecutionPipeline {
	return &ExecutionPipeline{}
}

// ApplyEnvironmentalChange executes side-effects triggered by a state mutation.
func (ep *ExecutionPipeline) ApplyEnvironmentalChange(state EnvironmentalState) error {
	ep.mu.Lock()
	defer ep.mu.Unlock()

	slog.Info("Applying Environmental Change", "state", state)

	// 1. Debounce Music Style Changes
	if state.MusicStyle != "" && state.MusicStyle != ep.lastMusicStyle {
		ep.lastMusicStyle = state.MusicStyle
		ep.triggerMusicProvider(state.MusicStyle)
	}

	// 2. Dispatch Client Telemetry (Eye animation parameters)
	if state.EyeColor != "" || state.EyeDynamics != "" {
		ep.dispatchClientTelemetry(state.EyeColor, state.EyeDynamics)
	}

	// 3. Play Immediate Sound Effects
	if len(state.TriggerKeywords) > 0 {
		ep.playTriggerSounds(state.TriggerKeywords)
	}

	return nil
}

func (ep *ExecutionPipeline) triggerMusicProvider(style string) {
	// NOTE: Integration point for external Media providers (Spotify/YT Music APIs)
	// or internal vraxter-media skills.
	slog.Info("🎧 Spawning async task: Playing music style", "style", style)
	go func(s string) {
		slog.Debug("Changing global playlist style", "style", s)
	}(style)
}

func (ep *ExecutionPipeline) dispatchClientTelemetry(color, dynamics string) {
	// NOTE: Integration point to push state to a WebSocket Hub or Server-Sent Events channel
	// connected to the client frontend.
	slog.Info("👁️ Dispatching telemetry to Client UI", "color", color, "dynamics", dynamics)
	go func(c, d string) {
		slog.Debug("Broadcasting telemetry payload", "color", c, "dynamics", d)
	}(color, dynamics)
}

func (ep *ExecutionPipeline) playTriggerSounds(keywords []string) {
	// NOTE: Casts to local IP speakers or plays via ALSA natively.
	slog.Info("🔊 Playing immediate trigger keywords", "keywords", keywords)
	go func(kws []string) {
		for _, kw := range kws {
			slog.Debug("Dispatching local sound effect playback", "keyword", kw)
		}
	}(keywords)
}
