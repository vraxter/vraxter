package core

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
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
	ID        string
	Title     string
	Status    string // "running", "completed", "failed"
	StartTime time.Time
}

type JobManager struct {
	mu          sync.RWMutex
	jobs        map[string]*JobDetails
	subscribers map[string]chan SystemEvent
}

func NewJobManager() *JobManager {
	return &JobManager{
		jobs:        make(map[string]*JobDetails),
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

// GetActiveJobs returns a list of all currently tracked jobs
func (jm *JobManager) GetActiveJobs() []JobDetails {
	jm.mu.RLock()
	defer jm.mu.RUnlock()
	
	var active []JobDetails
	for _, job := range jm.jobs {
		active = append(active, *job)
	}
	return active
}

func (jm *JobManager) SpawnDaemon(title string, work func() (string, error)) string {
	b := make([]byte, 4)
	rand.Read(b)
	jobID := "job-" + hex.EncodeToString(b)

	jm.mu.Lock()
	jm.jobs[jobID] = &JobDetails{
		ID:        jobID,
		Title:     title,
		Status:    "running",
		StartTime: time.Now(),
	}
	jm.mu.Unlock()

	jm.Broadcast(SystemEvent{
		Type:      EventJobStarted,
		JobID:     jobID,
		Title:     title,
		Timestamp: time.Now(),
	})

	go func() {
		output, err := work()
		
		jm.mu.Lock()
		if job, exists := jm.jobs[jobID]; exists {
			if err != nil {
				job.Status = "failed"
			} else {
				job.Status = "completed"
			}
		}
		jm.mu.Unlock()

		if err != nil {
			jm.Broadcast(SystemEvent{
				Type:      EventJobFailed,
				JobID:     jobID,
				Title:     title,
				Payload:   err.Error(),
				Timestamp: time.Now(),
			})
		} else {
			jm.Broadcast(SystemEvent{
				Type:      EventJobCompleted,
				JobID:     jobID,
				Title:     title,
				Payload:   output,
				Timestamp: time.Now(),
			})
		}
	}()

	return jobID
}
