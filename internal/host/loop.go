package host

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

const (
	defaultMaxConcurrent = 1
	idlePollInterval     = 30 * time.Second
	capacityPollInterval = 5 * time.Second
)

// LoopConfig holds configuration for an orchestration loop.
type LoopConfig struct {
	ProjectDir    string
	MaxConcurrent int
	StaggerDelay  time.Duration // delay between launching consecutive runs
}

// Loop manages a continuous orchestration loop for a project, keeping up to
// MaxConcurrent host workflow runs active at all times when work is available.
type Loop struct {
	config     LoopConfig
	runner     func(ctx context.Context, projectDir string) (*RunResult, error)
	store      ports.RunStore
	stopCh     chan struct{}
	mu         sync.Mutex
	running    bool
}

// NewLoop creates an orchestration loop. The runFn is called to start each
// orchestration run and should block until the run completes.
func NewLoop(cfg LoopConfig, store ports.RunStore, runFn func(ctx context.Context, projectDir string) (*RunResult, error)) *Loop {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	return &Loop{
		config: cfg,
		runner: runFn,
		store:  store,
	}
}

// Start begins the orchestration loop. No-op if already running.
func (l *Loop) Start() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running {
		return
	}
	l.running = true
	l.stopCh = make(chan struct{})
	go l.run()
	log.Printf("orchestration loop: started for %s (max_concurrent=%d, stagger=%s)", l.config.ProjectDir, l.config.MaxConcurrent, l.config.StaggerDelay)
}

// Stop signals the loop to stop. Active runs continue to completion.
func (l *Loop) Stop() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.running {
		return
	}
	l.running = false
	close(l.stopCh)
	log.Printf("orchestration loop: stopped for %s", l.config.ProjectDir)
}

// Running returns whether the loop is currently active.
func (l *Loop) Running() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.running
}

func (l *Loop) run() {
	type result struct {
		state domain.RunState
	}

	completions := make(chan result, l.config.MaxConcurrent)
	inFlight := 0

	for {
		// Fill up to max concurrent slots.
		launched := 0
		for inFlight < l.config.MaxConcurrent {
			select {
			case <-l.stopCh:
				return
			default:
			}

			// Check how many host runs are active for this project (in case of
			// leftover runs from previous loops).
			active := l.countActiveHostRuns()
			if active >= l.config.MaxConcurrent {
				break
			}

			// Stagger consecutive launches to avoid race conditions on task claiming.
			if launched > 0 && l.config.StaggerDelay > 0 {
				if !l.sleep(l.config.StaggerDelay) {
					return
				}
			}

			// Launch a new orchestration run in a goroutine.
			inFlight++
			launched++
			go func() {
				res, err := l.runner(context.Background(), l.config.ProjectDir)
				if err != nil {
					log.Printf("orchestration loop: run failed for %s: %v", l.config.ProjectDir, err)
					completions <- result{state: domain.RunStateFailed}
					return
				}
				completions <- result{state: res.State}
			}()
		}

		if inFlight == 0 {
			// Nothing in flight and at capacity — wait before checking again.
			if !l.sleep(capacityPollInterval) {
				return
			}
			continue
		}

		// Wait for any run to complete.
		select {
		case r := <-completions:
			inFlight--
			if r.state == domain.RunStateSucceeded {
				// Work was done — try to fill slots immediately.
				continue
			}
			// Run failed or aborted (no tasks available). Back off.
			if !l.sleep(idlePollInterval) {
				return
			}
		case <-l.stopCh:
			return
		}
	}
}

// countActiveHostRuns counts running or pending host runs for the project.
func (l *Loop) countActiveHostRuns() int {
	ctx := context.Background()
	runs, err := l.store.ListRunsByProject(ctx, l.config.ProjectDir, time.Time{})
	if err != nil {
		return 0
	}
	count := 0
	for _, r := range runs {
		if r.IsHost && (r.State == domain.RunStatePending || r.State == domain.RunStateRunning) {
			count++
		}
	}
	return count
}

// sleep waits for the given duration or until the loop is stopped.
// Returns false if the loop was stopped.
func (l *Loop) sleep(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-l.stopCh:
		return false
	}
}
