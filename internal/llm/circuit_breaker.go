// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package llm

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"time"
)

// CircuitState represents the state of a DDIL provider/model circuit breaker.
type CircuitState string

const (
	CircuitClosed   CircuitState = "Closed"
	CircuitOpen     CircuitState = "Open"
	CircuitHalfOpen CircuitState = "HalfOpen"
)

// CircuitBreakerConfig defines the failure thresholds and cooldown parameters for a CircuitBreaker.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures before opening the circuit (default: 3).
	FailureThreshold int
	// SuccessThreshold is the number of consecutive successes in HalfOpen required to close the circuit (default: 1).
	SuccessThreshold int
	// Cooldown is the base duration the circuit remains Open before allowing a HalfOpen probe (default: 15s).
	Cooldown time.Duration
	// JitterFraction bounds the random cooldown jitter in [0, JitterFraction * Cooldown].
	// Set to 0 (or negative) to disable jitter for deterministic testing.
	JitterFraction float64
	// Clock overrides time.Now for deterministic unit testing.
	Clock func() time.Time
}

// DefaultCircuitBreakerConfig returns sensible DDIL circuit breaker defaults.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 3,
		SuccessThreshold: 1,
		Cooldown:         15 * time.Second,
		JitterFraction:   0.15,
		Clock:            time.Now,
	}
}

// CircuitMetrics provides observable telemetry for a provider/model circuit breaker.
type CircuitMetrics struct {
	State                CircuitState
	ConsecutiveFailures  int
	ConsecutiveSuccesses int
	TotalFailures        int64
	TotalSuccesses       int64
	RejectedCount        int64
	OpenTransitions      int64
	OpenedAt             time.Time
	EffectiveCooldown    time.Duration
}

// CircuitBreaker protects LLM calls in degraded DDIL links by failing fast when a provider/model
// experiences repeated timeouts or errors.
type CircuitBreaker struct {
	mu                   sync.Mutex
	cfg                  CircuitBreakerConfig
	state                CircuitState
	consecutiveFailures  int
	consecutiveSuccesses int
	totalFailures        int64
	totalSuccesses       int64
	rejectedCount        int64
	openTransitions      int64
	openedAt             time.Time
	effectiveCooldown    time.Duration
	halfOpenInFlight     bool
}

// NewCircuitBreaker creates a CircuitBreaker initialized in the Closed state.
func NewCircuitBreaker(cfg CircuitBreakerConfig) *CircuitBreaker {
	if cfg.FailureThreshold <= 0 {
		cfg.FailureThreshold = 3
	}
	if cfg.SuccessThreshold <= 0 {
		cfg.SuccessThreshold = 1
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 15 * time.Second
	}
	if cfg.Clock == nil {
		cfg.Clock = time.Now
	}
	return &CircuitBreaker{
		cfg:   cfg,
		state: CircuitClosed,
	}
}

func (cb *CircuitBreaker) computeCooldownWithJitter() time.Duration {
	base := cb.cfg.Cooldown
	if cb.cfg.JitterFraction <= 0 {
		return base
	}
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return base
	}
	u := binary.LittleEndian.Uint64(b[:])
	frac := (float64(u%10000) / 10000.0) * cb.cfg.JitterFraction
	return base + time.Duration(float64(base)*frac)
}

// Allow checks whether a request is permitted to proceed through the circuit breaker.
// If the circuit is Open and its cooldown has elapsed, it transitions to HalfOpen and permits a probe.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true

	case CircuitOpen:
		now := cb.cfg.Clock()
		if now.Sub(cb.openedAt) >= cb.effectiveCooldown {
			cb.state = CircuitHalfOpen
			cb.consecutiveSuccesses = 0
			cb.halfOpenInFlight = true
			return true
		}
		cb.rejectedCount++
		return false

	case CircuitHalfOpen:
		// Allow probe request in HalfOpen
		cb.halfOpenInFlight = true
		return true

	default:
		return true
	}
}

// RecordSuccess records a successful provider response, transitioning HalfOpen -> Closed
// once SuccessThreshold is met.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.totalSuccesses++
	cb.consecutiveFailures = 0
	cb.halfOpenInFlight = false

	switch cb.state {
	case CircuitHalfOpen:
		cb.consecutiveSuccesses++
		if cb.consecutiveSuccesses >= cb.cfg.SuccessThreshold {
			cb.state = CircuitClosed
			cb.consecutiveSuccesses = 0
		}
	case CircuitOpen:
		cb.state = CircuitClosed
		cb.consecutiveSuccesses = 0
	case CircuitClosed:
		cb.consecutiveSuccesses++
	}
}

// RecordFailure records a provider error or timeout. If the breaker is in HalfOpen or
// consecutive failures reach FailureThreshold, the circuit transitions to Open.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.totalFailures++
	cb.consecutiveFailures++
	cb.consecutiveSuccesses = 0
	cb.halfOpenInFlight = false

	if cb.state == CircuitHalfOpen || cb.consecutiveFailures >= cb.cfg.FailureThreshold {
		if cb.state != CircuitOpen {
			cb.openTransitions++
		}
		cb.state = CircuitOpen
		cb.openedAt = cb.cfg.Clock()
		cb.effectiveCooldown = cb.computeCooldownWithJitter()
	}
}

// State returns the current CircuitState (Closed, Open, or HalfOpen).
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// Metrics returns a point-in-time snapshot of the circuit breaker's state and counters.
func (cb *CircuitBreaker) Metrics() CircuitMetrics {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return CircuitMetrics{
		State:                cb.state,
		ConsecutiveFailures:  cb.consecutiveFailures,
		ConsecutiveSuccesses: cb.consecutiveSuccesses,
		TotalFailures:        cb.totalFailures,
		TotalSuccesses:       cb.totalSuccesses,
		RejectedCount:        cb.rejectedCount,
		OpenTransitions:      cb.openTransitions,
		OpenedAt:             cb.openedAt,
		EffectiveCooldown:    cb.effectiveCooldown,
	}
}

// Reset resets the circuit breaker back to Closed with zeroed failure counters.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.consecutiveFailures = 0
	cb.consecutiveSuccesses = 0
	cb.halfOpenInFlight = false
}
