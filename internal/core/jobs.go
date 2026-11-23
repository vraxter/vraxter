// Copyright (c) 2026 Vraxter. All rights reserved.
//
// This file is part of Vraxter.
// Vraxter is free software licensed under the GNU Affero General Public License (AGPL) v3.0.
// See the LICENSE file in the project root for full license information.
//
// For commercial licensing inquiries, contact Vraxter.

package core

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

const (
	// DefaultMaxRetainedJobs caps finished job metadata kept in RAM to prevent unbounded growth.
	DefaultMaxRetainedJobs = 256
	// DefaultMaxJobAge purges finished job entries older than 24 hours.
	DefaultMaxJobAge = 24 * time.Hour
)

type EventType int

const (
	EventJobStarted EventType = iota
	EventJobCompleted
	EventJobFailed
	EventSwarmProgress
	EventSystemAlert
	EventPlanProposed
)

type SystemEvent struct {
	Type      EventType
	JobID     string
	Title     string
	Payload   string
	Timestamp time.Time
}

type JobDetails struct {
	ID          string
	Title       string
	Status      string // "running", "completed", "failed"
	StartTime   time.Time
	CompletedAt time.Time
}

type JobManager struct {
	mu          sync.RWMutex
	jobs        map[string]*JobDetails
	jobOrder    []string // FIFO insertion order for bounded ring-buffer eviction
	maxJobs     int
	maxAge      time.Duration
	subscribers map[string]chan SystemEvent
}

func NewJobManager() *JobManager {
	return NewJobManagerWithLimit(DefaultMaxRetainedJobs)
}

// NewJobManagerWithLimit creates a JobManager with an explicit maximum retained jobs cap.
func NewJobManagerWithLimit(maxJobs int) *JobManager {
	if maxJobs <= 0 {
		maxJobs = DefaultMaxRetainedJobs
	}
	return &JobManager{
		jobs:        make(map[string]*JobDetails),
		jobOrder:    make([]string, 0, maxJobs),
		maxJobs:     maxJobs,
		maxAge:      DefaultMaxJobAge,
		subscribers: make(map[string]chan SystemEvent),
	}
}

func (jm *JobManager) Subscribe(clientID string) <-chan SystemEvent {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	ch := make(chan SystemEvent, 100)
	jm.subscribers[clientID] = ch
	return ch
}

func (jm *JobManager) Unsubscribe(clientID string) {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if ch, ok := jm.subscribers[clientID]; ok {
		close(ch)
		delete(jm.subscribers, clientID)
	}
}

func (jm *JobManager) Broadcast(event SystemEvent) {
	jm.mu.RLock()
	defer jm.mu.RUnlock()
	for _, ch := range jm.subscribers {
		select {
		case ch <- event:
		default:
			// drop if full
		}
	}
}

// pruneLocked enforces bounded retention on jm.jobs: removes expired finished jobs first,
// then evicts oldest finished jobs in FIFO order until len(jm.jobs) <= jm.maxJobs.
// Active ("running") jobs are preserved.
func (jm *JobManager) pruneLocked(now time.Time) {
	if jm.maxJobs <= 0 {
		jm.maxJobs = DefaultMaxRetainedJobs
	}
	if jm.maxAge <= 0 {
		jm.maxAge = DefaultMaxJobAge
	}

	// Pass 1: Purge expired completed/failed jobs
	kept := jm.jobOrder[:0]
	for _, id := range jm.jobOrder {
		job, exists := jm.jobs[id]
		if !exists {
			continue
		}
		if job.Status != "running" && !job.CompletedAt.IsZero() && now.Sub(job.CompletedAt) > jm.maxAge {
			delete(jm.jobs, id)
			continue
		}
		kept = append(kept, id)
	}
	jm.jobOrder = kept

	// Pass 2: Evict oldest finished jobs if count still exceeds maxJobs
	if len(jm.jobs) <= jm.maxJobs {
		return
	}

	kept = jm.jobOrder[:0]
	for _, id := range jm.jobOrder {
		job, exists := jm.jobs[id]
		if !exists {
			continue
		}
		if len(jm.jobs) > jm.maxJobs && job.Status != "running" {
			delete(jm.jobs, id)
			continue
		}
		kept = append(kept, id)
	}
	jm.jobOrder = kept
}

// GetActiveJobs returns a list of all currently tracked jobs
func (jm *JobManager) GetActiveJobs() []JobDetails {
	jm.mu.RLock()
	defer jm.mu.RUnlock()

	active := make([]JobDetails, 0, len(jm.jobs))
	for _, id := range jm.jobOrder {
		if job, ok := jm.jobs[id]; ok {
			active = append(active, *job)
		}
	}
	return active
}

func (jm *JobManager) SpawnDaemon(title string, work func() (string, error)) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	jobID := "job-" + hex.EncodeToString(b)

	now := time.Now()
	jm.mu.Lock()
	jm.jobs[jobID] = &JobDetails{
		ID:        jobID,
		Title:     title,
		Status:    "running",
		StartTime: now,
	}
	jm.jobOrder = append(jm.jobOrder, jobID)
	jm.pruneLocked(now)
	jm.mu.Unlock()

	jm.Broadcast(SystemEvent{
		Type:      EventJobStarted,
		JobID:     jobID,
		Title:     title,
		Timestamp: now,
	})

	go func() {
		output, err := work()

		doneAt := time.Now()
		jm.mu.Lock()
		if job, exists := jm.jobs[jobID]; exists {
			if err != nil {
				job.Status = "failed"
			} else {
				job.Status = "completed"
			}
			job.CompletedAt = doneAt
		}
		jm.pruneLocked(doneAt)
		jm.mu.Unlock()

		if err != nil {
			jm.Broadcast(SystemEvent{
				Type:      EventJobFailed,
				JobID:     jobID,
				Title:     title,
				Payload:   err.Error(),
				Timestamp: doneAt,
			})
		} else {
			jm.Broadcast(SystemEvent{
				Type:      EventJobCompleted,
				JobID:     jobID,
				Title:     title,
				Payload:   output,
				Timestamp: doneAt,
			})
		}
	}()

	return jobID
}
