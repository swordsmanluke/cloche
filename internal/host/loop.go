package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
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

// ListTasksFunc is called to discover available tasks. It should run the
// list-tasks workflow (or equivalent) and return the parsed tasks.
type ListTasksFunc func(ctx context.Context, projectDir string) ([]Task, error)

// MainFunc is called to execute the main workflow for a given task.
type MainFunc func(ctx context.Context, projectDir string, taskID string) (*RunResult, error)

// FinalizeFunc is called after the main workflow completes (both success and
// failure). It receives the task ID and the outcome of the main phase.
type FinalizeFunc func(ctx context.Context, projectDir string, taskID string, mainResult *RunResult) (*RunResult, error)

// RunFunc is the legacy function signature for launching a host workflow run.
// When taskID is non-empty, the runner should propagate it as CLOCHE_TASK_ID.
type RunFunc func(ctx context.Context, projectDir string, taskID string) (*RunResult, error)

// Loop manages a continuous orchestration loop for a project, keeping up to
// MaxConcurrent host workflow runs active at all times when work is available.
//
// When configured with three-phase functions (list-tasks, main, finalize), the
// loop discovers tasks via list-tasks, picks open ones, runs main for each, and
// then runs finalize on completion. When configured with a legacy RunFunc, it
// falls back to the single-function approach.
type Loop struct {
	config     LoopConfig
	listTasks  ListTasksFunc
	mainFn     MainFunc
	finalizeFn FinalizeFunc // optional; skipped if nil
	runner     RunFunc      // legacy single-function mode
	store      ports.RunStore
	assigner   TaskAssigner // optional; legacy task assignment
	stopCh     chan struct{}
	mu         sync.Mutex
	running    bool
	dedupMu    sync.Mutex
	dedup      map[string]time.Time // task ID -> last assignment time
}

// NewLoop creates an orchestration loop using a single run function (legacy mode).
// For three-phase orchestration, use NewPhaseLoop instead.
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

// NewPhaseLoop creates a three-phase orchestration loop. The listTasksFn
// discovers available tasks, mainFn executes the main workflow for a task,
// and finalizeFn (optional) runs cleanup after main completes.
func NewPhaseLoop(cfg LoopConfig, store ports.RunStore, listTasksFn ListTasksFunc, mainFn MainFunc, finalizeFn FinalizeFunc) *Loop {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.DedupTimeout <= 0 {
		cfg.DedupTimeout = defaultDedupTimeout
	}
	return &Loop{
		config:     cfg,
		listTasks:  listTasksFn,
		mainFn:     mainFn,
		finalizeFn: finalizeFn,
		store:      store,
		dedup:      make(map[string]time.Time),
	}
}

// SetTaskAssigner configures a TaskAssigner for daemon-managed task assignment.
// Only used in legacy (single RunFunc) mode when no list-tasks function is set.
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
	if l.listTasks != nil && l.mainFn != nil {
		l.runPhased()
	} else {
		l.runLegacy()
	}
}

// runPhased implements the three-phase orchestration loop:
// 1. list-tasks → discover available work
// 2. main → execute work for each open task (up to MaxConcurrent)
// 3. finalize → cleanup after main completes (on both success and failure)
func (l *Loop) runPhased() {
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

			// Check how many host runs are active for this project.
			active := l.countActiveHostRuns()
			if active >= l.config.MaxConcurrent {
				break
			}

			// Stagger consecutive launches.
			if launched > 0 && l.config.StaggerDelay > 0 {
				if !l.sleep(l.config.StaggerDelay) {
					return
				}
			}

			// Phase 1: list-tasks — discover available work.
			taskID, ok := l.pickTaskFromPhase()
			if !ok {
				break // no assignable tasks
			}

			// Launch main + finalize in a goroutine.
			inFlight++
			launched++
			tid := taskID
			go func() {
				// Phase 2: main — do the work.
				mainResult, err := l.mainFn(context.Background(), l.config.ProjectDir, tid)
				if err != nil {
					log.Printf("orchestration loop: main failed for %s task %s: %v", l.config.ProjectDir, tid, err)
					if mainResult == nil {
						mainResult = &RunResult{State: domain.RunStateFailed}
					}
				}

				// Phase 3: finalize — cleanup (runs on both success and failure).
				if l.finalizeFn != nil {
					_, finalizeErr := l.finalizeFn(context.Background(), l.config.ProjectDir, tid, mainResult)
					if finalizeErr != nil {
						log.Printf("orchestration loop: finalize failed for %s task %s: %v", l.config.ProjectDir, tid, finalizeErr)
					}
				}

				completions <- result{state: mainResult.State}
			}()
		}

		if inFlight == 0 {
			// Nothing in flight and couldn't launch anything — wait before checking again.
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
			// Run failed or aborted. Back off.
			if !l.sleep(idlePollInterval) {
				return
			}
		case <-l.stopCh:
			return
		}
	}
}

// pickTaskFromPhase runs the list-tasks function and picks the first open,
// non-deduped task.
func (l *Loop) pickTaskFromPhase() (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tasks, err := l.listTasks(ctx, l.config.ProjectDir)
	if err != nil {
		log.Printf("orchestration loop: list-tasks failed for %s: %v", l.config.ProjectDir, err)
		return "", false
	}

	now := time.Now()
	l.dedupMu.Lock()
	defer l.dedupMu.Unlock()

	// Expire old dedup entries.
	for id, ts := range l.dedup {
		if now.Sub(ts) >= l.config.DedupTimeout {
			delete(l.dedup, id)
		}
	}

	for _, task := range tasks {
		if task.ID == "" {
			continue
		}
		if !task.IsOpen() {
			continue
		}
		if ts, ok := l.dedup[task.ID]; ok && now.Sub(ts) < l.config.DedupTimeout {
			continue
		}
		l.dedup[task.ID] = now
		log.Printf("orchestration loop: assigned task %s for %s", task.ID, l.config.ProjectDir)
		return task.ID, true
	}

	return "", false
}

// runLegacy implements the original single-function orchestration loop.
func (l *Loop) runLegacy() {
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

// ReadListTasksOutput reads the output directory from a list-tasks workflow run
// and parses JSONL task output from the last step's output file.
func ReadListTasksOutput(outputDir string) ([]Task, error) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil, fmt.Errorf("reading list-tasks output dir: %w", err)
	}

	// Find the most recently modified .out file.
	var latest string
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".out" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if latest == "" || info.ModTime().After(latestMod) {
			latest = e.Name()
			latestMod = info.ModTime()
		}
	}

	if latest == "" {
		return nil, fmt.Errorf("no output files found in %s", outputDir)
	}

	data, err := os.ReadFile(filepath.Join(outputDir, latest))
	if err != nil {
		return nil, fmt.Errorf("reading output file %s: %w", latest, err)
	}

	return ParseTasksJSONL(string(data))
}
