package core

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/patagonicrune/vraxter/internal/llm"
)

// SwarmTask represents a specific assignment for a specialist.
type SwarmTask struct {
	SpecialistID string
	Query        string
}

// SwarmDispatcher handles parallel execution of multiple autonomous agents.
type SwarmDispatcher struct {
	engine     *ExecutionEngine
	jobManager *JobManager
	resolver   *IntentResolver
}

func NewSwarmDispatcher(engine *ExecutionEngine, jobManager *JobManager, resolver *IntentResolver) *SwarmDispatcher {
	return &SwarmDispatcher{
		engine:     engine,
		jobManager: jobManager,
		resolver:   resolver,
	}
}

// Dispatch kicks off parallel execution of multiple specialists for a given set of tasks.
// It runs entirely asynchronously within the JobManager daemon system.
func (s *SwarmDispatcher) Dispatch(sessionID string, swarmTitle string, tasks []SwarmTask) string {
	return s.jobManager.SpawnDaemon(swarmTitle, func() (string, error) {
		var wg sync.WaitGroup
		results := make(chan string, len(tasks))
		errs := make(chan error, len(tasks))

		for _, t := range tasks {
			wg.Add(1)
			go func(task SwarmTask) {
				defer wg.Done()
				
				spec, err := s.engine.SpecRepo.GetSpecialist(task.SpecialistID)
				displayName := task.SpecialistID
				if err == nil && spec != nil {
					displayName = spec.Name
				}

				s.jobManager.Broadcast(SystemEvent{
					Type:      EventSwarmProgress,
					Title:     swarmTitle,
					Payload:   fmt.Sprintf("Delegating sub-task to %s...", displayName),
					Timestamp: time.Now(),
				})

				ctx := context.Background()

				if s.engine.SwarmDelegate != nil {
					// We need a dummy channel to swallow intermediate tokens so they don't block
					outDrain := make(chan llm.StreamEvent, 100)
					go func() {
						for range outDrain {
						}
					}()
					
					swarmResult := s.engine.SwarmDelegate(ctx, outDrain, task.SpecialistID, task.Query, sessionID)
					results <- fmt.Sprintf("=== Result from %s ===\n%s", displayName, swarmResult)
				} else {
					errs <- fmt.Errorf("swarm delegate not wired in engine")
				}
			}(t)
		}

		wg.Wait()
		close(results)
		close(errs)

		var finalOutput string
		for r := range results {
			finalOutput += r + "\n\n"
		}
		
		var finalErr error
		for e := range errs {
			if finalErr == nil {
				finalErr = e
			}
		}

		if finalErr != nil {
			return finalOutput, finalErr
		}

		return finalOutput, nil
	})
}
