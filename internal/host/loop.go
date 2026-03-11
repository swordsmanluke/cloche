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
	defaultDedupTimeout  = 5 * time.Minute
	idlePollInterval     = 30 * time.Second
	capacityPollInterval = 5 * time.Second
)

// LoopConfig holds configuration for an orchestration loop.
type LoopConfig struct {
	ProjectDir    string
	MaxConcurrent int
	StaggerDelay  time.Duration // delay between launching consecutive runs
	DedupTimeout  time.Duration // how long to suppress reassignment of the same task ID
}

// RunFunc is the function signature for launching a host workflow run.
// When taskID is non-empty, the runner should propagate it as CLOCHE_TASK_ID.
type RunFunc func(ctx context.Context, projectDir string, taskID string) (*RunResult, error)

// Loop manages a continuous orchestration loop for a project, keeping up to
// MaxConcurrent host workflow runs active at all times when work is available.
type Loop struct {
	config   LoopConfig
	runner   RunFunc
	store    ports.RunStore
	assigner TaskAssigner // optional; when set, daemon picks tasks
	stopCh   chan struct{}
	mu       sync.Mutex
	running  bool
	dedupMu  sync.Mutex
	dedup    map[string]time.Time // task ID -> last assignment time
}

// NewLoop creates an orchestration loop. The runFn is called to start each
// orchestration run and should block until the run completes.
func NewLoop(cfg LoopConfig, store ports.RunStore, runFn RunFunc) *Loop {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.DedupTimeout <= 0 {
		cfg.DedupTimeout = defaultDedupTimeout
	}
	return &Loop{
		config: cfg,
		runner: runFn,
		store:  store,
		dedup:  make(map[string]time.Time),
	}
}

// SetTaskAssigner configures a TaskAssigner for daemon-managed task assignment.
// When set, the loop will query for available tasks and pass the selected task
// ID to the runner, preventing concurrent assignment of the same task.
func (l *Loop) SetTaskAssigner(a TaskAssigner) {
	l.assigner = a
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

			// Determine task ID (if task assigner is configured).
			taskID := ""
			if l.assigner != nil {
				var ok bool
				taskID, ok = l.pickTask()
				if !ok {
					// No assignable tasks available.
					break
				}
			}

			// Launch a new orchestration run in a goroutine.
			inFlight++
			launched++
			tid := taskID // capture for goroutine
			go func() {
				res, err := l.runner(context.Background(), l.config.ProjectDir, tid)
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

// pickTask queries the task assigner for available tasks, filters out those
// within the dedup timeout window, and returns the first assignable task ID.
// It records the assignment time so the same task won't be picked again until
// the dedup timeout expires. Returns ("", false) if no tasks are available.
func (l *Loop) pickTask() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tasks, err := l.assigner.ListTasks(ctx, l.config.ProjectDir)
	if err != nil {
		log.Printf("orchestration loop: list-tasks failed for %s: %v", l.config.ProjectDir, err)
		return "", false
	}

	now := time.Now()
	l.dedupMu.Lock()
	defer l.dedupMu.Unlock()

	// Expire old dedup entries while we're here.
	for id, ts := range l.dedup {
		if now.Sub(ts) >= l.config.DedupTimeout {
			delete(l.dedup, id)
		}
	}

	for _, task := range tasks {
		if task.ID == "" {
			continue
		}
		if ts, ok := l.dedup[task.ID]; ok && now.Sub(ts) < l.config.DedupTimeout {
			continue // still within dedup window
		}
		// Assign this task.
		l.dedup[task.ID] = now
		log.Printf("orchestration loop: assigned task %s for %s", task.ID, l.config.ProjectDir)
		return task.ID, true
	}

	return "", false
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
