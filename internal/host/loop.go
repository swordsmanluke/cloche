package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

// AttemptIDMetadataKey is the gRPC metadata key used to propagate an attempt ID
// from a host executor to a child container dispatch.
const AttemptIDMetadataKey = "x-cloche-attempt-id"

const (
	defaultMaxConcurrent          = 1
	defaultDedupTimeout           = 5 * time.Minute
	defaultMaxConsecutiveFailures = 3
	idlePollInterval              = 2 * time.Minute
	capacityPollInterval          = 30 * time.Second
)

// LoopConfig holds configuration for an orchestration loop.
type LoopConfig struct {
	ProjectDir             string
	MaxConcurrent          int
	StaggerDelay           time.Duration // delay between launching consecutive runs
	DedupTimeout           time.Duration // how long to suppress reassignment of the same task ID
	StopOnError            bool          // halt loop on unrecovered error (allow in-flight work to finish)
	MaxConsecutiveFailures int           // halt loop after N consecutive failures (default: 3, must be > 0)
}

// ListTasksFunc is called to discover available tasks. It should run the
// list-tasks workflow (or equivalent) and return the parsed tasks.
type ListTasksFunc func(ctx context.Context, projectDir string) ([]Task, error)

// MainFunc is called to execute the main workflow for a given task.
// attemptID is the ID of the Attempt record created for this loop iteration.
type MainFunc func(ctx context.Context, projectDir string, taskID string, taskTitle string, attemptID string) (*RunResult, error)

// FinalizeFunc is called after the main workflow completes (both success and
// failure). It receives the task ID, attempt ID, and the outcome of the main phase.
type FinalizeFunc func(ctx context.Context, projectDir string, taskID string, attemptID string, mainResult *RunResult) (*RunResult, error)

// RunFunc is the legacy function signature for launching a host workflow run.
// When taskID is non-empty, the runner should propagate it as CLOCHE_TASK_ID.
// attemptID is the ID of the Attempt record for this run (may be empty).
type RunFunc func(ctx context.Context, projectDir string, taskID string, attemptID string) (*RunResult, error)

// TaskAssignment tracks the assignment state of a task.
type TaskAssignment struct {
	AssignedAt time.Time
	RunID      string // run ID handling this task (set after main completes)
}

// TaskStateEntry combines a task with its assignment state.
type TaskStateEntry struct {
	Task       Task
	Assigned   bool
	AssignedAt time.Time
	RunID      string // empty if not yet known
}

// Loop manages a continuous orchestration loop for a project, keeping up to
// MaxConcurrent host workflow runs active at all times when work is available.
//
// The loop uses a three-phase approach: list-tasks discovers available work,
// main executes the work for each task, and finalize runs cleanup after each
// main run (on both success and failure). When no list-tasks function is
// configured (e.g. via NewLoop without a task assigner), an untracked sentinel
// task is used so main runs continuously.
type Loop struct {
	config       LoopConfig
	listTasks    ListTasksFunc
	mainFn       MainFunc
	finalizeFn   FinalizeFunc // optional; skipped if nil
	store        ports.RunStore
	attemptStore ports.AttemptStore   // optional; creates Attempt records when set
	taskStore    ports.TaskStore      // optional; ensures Task records exist when set
	activityLog  *activitylog.Logger  // optional; records attempt lifecycle events
	assigner     TaskAssigner         // optional; feeds listTasks in NewLoop-created loops
	stopCh       chan struct{}
	resumeCh     chan struct{} // signaled by Resume() to wake the loop
	mu           sync.Mutex
	running      bool
	haltedMu            sync.RWMutex
	halted              bool   // true when loop is halted due to an unrecovered error
	haltError           string // the error message that caused the halt
	consecutiveFailures int    // number of consecutive failed runs
	dedupMu             sync.Mutex
	dedup      map[string]time.Time // task ID -> last assignment time
	tasksMu    sync.RWMutex
	lastTasks  []Task                    // most recently fetched tasks
	taskRuns   map[string]TaskAssignment // task ID -> assignment info
}

// NewLoop creates an orchestration loop using a single run function. Internally
// it uses the three-phase approach: if a task assigner is configured via
// SetTaskAssigner, tasks are discovered through it and their IDs passed to
// runFn; without a task assigner the loop runs runFn continuously with an empty
// task ID (no task-level tracking).
func NewLoop(cfg LoopConfig, store ports.RunStore, runFn RunFunc) *Loop {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = defaultMaxConcurrent
	}
	if cfg.DedupTimeout <= 0 {
		cfg.DedupTimeout = defaultDedupTimeout
	}
	if cfg.MaxConsecutiveFailures <= 0 {
		cfg.MaxConsecutiveFailures = defaultMaxConsecutiveFailures
	}
	l := &Loop{
		config:   cfg,
		store:    store,
		resumeCh: make(chan struct{}, 1),
		dedup:    make(map[string]time.Time),
		taskRuns: make(map[string]TaskAssignment),
	}
	// listTasks delegates to the assigner when set, otherwise returns a single
	// untracked sentinel task (empty ID) so main runs unconditionally.
	l.listTasks = func(ctx context.Context, projectDir string) ([]Task, error) {
		if l.assigner != nil {
			return l.assigner.ListTasks(ctx, projectDir)
		}
		return []Task{{ID: ""}}, nil
	}
	l.mainFn = func(ctx context.Context, projectDir string, taskID string, _ string, attemptID string) (*RunResult, error) {
		return runFn(ctx, projectDir, taskID, attemptID)
	}
	return l
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
	if cfg.MaxConsecutiveFailures <= 0 {
		cfg.MaxConsecutiveFailures = defaultMaxConsecutiveFailures
	}
	return &Loop{
		config:     cfg,
		listTasks:  listTasksFn,
		mainFn:     mainFn,
		finalizeFn: finalizeFn,
		store:      store,
		resumeCh:   make(chan struct{}, 1),
		dedup:      make(map[string]time.Time),
		taskRuns:   make(map[string]TaskAssignment),
	}
}

// SetTaskAssigner configures a TaskAssigner for task discovery in loops created
// with NewLoop. Must be called before Start.
func (l *Loop) SetTaskAssigner(a TaskAssigner) {
	l.assigner = a
}

// SetAttemptStore configures an AttemptStore so the loop creates and completes
// Attempt records for each task it picks up.
func (l *Loop) SetAttemptStore(a ports.AttemptStore) {
	l.attemptStore = a
}

// SetTaskStore configures a TaskStore so the loop can ensure Task records exist
// before creating Attempt records against them.
func (l *Loop) SetTaskStore(ts ports.TaskStore) {
	l.taskStore = ts
}

// SetActivityLogger attaches an activity logger so the loop records attempt
// lifecycle events (started/ended) to the project's .cloche/activity.log.
func (l *Loop) SetActivityLogger(al *activitylog.Logger) {
	l.activityLog = al
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

// Halted returns whether the loop is halted due to an unrecovered error,
// along with the error message that caused the halt. When halted, the loop
// is still running but will not pick up new work.
func (l *Loop) Halted() (bool, string) {
	l.haltedMu.RLock()
	defer l.haltedMu.RUnlock()
	return l.halted, l.haltError
}

// Resume clears the halted state so the loop resumes picking up new work.
func (l *Loop) Resume() {
	l.haltedMu.Lock()
	defer l.haltedMu.Unlock()
	if l.halted {
		log.Printf("orchestration loop: resumed for %s (was halted: %s)", l.config.ProjectDir, l.haltError)
		l.halted = false
		l.haltError = ""
		l.consecutiveFailures = 0
		// Wake the loop from any sleep so it picks up work immediately.
		select {
		case l.resumeCh <- struct{}{}:
		default:
		}
	}
}

// halt sets the loop into halted state with the given error message.
func (l *Loop) halt(errMsg string) {
	l.haltedMu.Lock()
	defer l.haltedMu.Unlock()
	l.halted = true
	l.haltError = errMsg
	log.Printf("orchestration loop: halted for %s (stop_on_error): %s", l.config.ProjectDir, errMsg)
}

// recordConsecutiveFailure increments the consecutive failure counter and
// returns true if the threshold has been reached.
func (l *Loop) recordConsecutiveFailure() bool {
	l.haltedMu.Lock()
	defer l.haltedMu.Unlock()
	if l.halted {
		return false // already halted (e.g. by StopOnError)
	}
	l.consecutiveFailures++
	return l.consecutiveFailures >= l.config.MaxConsecutiveFailures
}

// resetConsecutiveFailures resets the consecutive failure counter to zero.
func (l *Loop) resetConsecutiveFailures() {
	l.haltedMu.Lock()
	defer l.haltedMu.Unlock()
	l.consecutiveFailures = 0
}

// isHalted returns true if the loop is currently halted.
func (l *Loop) isHalted() bool {
	l.haltedMu.RLock()
	defer l.haltedMu.RUnlock()
	return l.halted
}

func (l *Loop) run() {
	l.runPhased()
}

// runPhased implements the three-phase orchestration loop:
// 1. list-tasks → discover available work
// 2. main → execute work for each open task (up to MaxConcurrent)
// 3. finalize → cleanup after main completes (on both success and failure)
func (l *Loop) runPhased() {
	type result struct {
		state  domain.RunState
		errMsg string
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

			// If halted, don't pick up new work.
			if l.isHalted() {
				break
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
			taskID, taskTitle, ok := l.pickTaskFromPhase()
			if !ok {
				break // no assignable tasks
			}

			// Create an Attempt record for this loop iteration.
			attemptID := l.createAttemptForTask(taskID, taskTitle, l.config.ProjectDir)

			// Launch main + finalize in a goroutine.
			inFlight++
			launched++
			tid := taskID
			ttitle := taskTitle
			aid := attemptID
			go func() {
				// overallState defaults to failed; the deferred function guarantees
				// completeAttempt and the completions signal are always sent, even on
				// panic, so no attempt can be left permanently stuck as 'running'.
				overallState := domain.RunStateFailed
				defer func() {
					if r := recover(); r != nil {
						log.Printf("orchestration loop: panic in task %s goroutine: %v", tid, r)
					}
					l.completeAttempt(aid, overallState)
					completions <- result{state: overallState, errMsg: fmt.Sprintf("task %s failed", tid)}
				}()

				// Phase 2: main — do the work.
				mainResult, err := l.mainFn(context.Background(), l.config.ProjectDir, tid, ttitle, aid)
				if err != nil {
					log.Printf("orchestration loop: main failed for %s task %s: %v", l.config.ProjectDir, tid, err)
					if mainResult == nil {
						mainResult = &RunResult{State: domain.RunStateFailed}
					}
				}
				if mainResult == nil {
					mainResult = &RunResult{State: domain.RunStateFailed}
				}

				// Track the run ID for this task and persist the task title.
				if mainResult.RunID != "" {
					l.tasksMu.Lock()
					l.taskRuns[tid] = TaskAssignment{
						AssignedAt: time.Now(),
						RunID:      mainResult.RunID,
					}
					l.tasksMu.Unlock()

					if ttitle != "" {
						if r, err := l.store.GetRun(context.Background(), mainResult.RunID); err == nil && r.TaskTitle == "" {
							r.TaskTitle = ttitle
							_ = l.store.UpdateRun(context.Background(), r)
						}
					}
				}

				// Phase 3: finalize — cleanup (runs on both success and failure).
				// The overall state is the worst of main and finalize outcomes.
				overallState = mainResult.State
				if l.finalizeFn != nil {
					finalizeResult, finalizeErr := l.finalizeFn(context.Background(), l.config.ProjectDir, tid, aid, mainResult)
					if finalizeErr != nil {
						log.Printf("orchestration loop: finalize failed for %s task %s: %v", l.config.ProjectDir, tid, finalizeErr)
						overallState = domain.WorseState(overallState, domain.RunStateFailed)
					} else if finalizeResult != nil {
						overallState = domain.WorseState(overallState, finalizeResult.State)
					}
				}
				// defer handles completeAttempt and completions
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
				l.resetConsecutiveFailures()
				// Work was done — try to fill slots immediately.
				continue
			}
			// Run failed or aborted.
			if l.config.StopOnError {
				l.halt(r.errMsg)
			}
			if l.recordConsecutiveFailure() {
				l.halt(fmt.Sprintf("halted after %d consecutive failures: %s", l.config.MaxConsecutiveFailures, r.errMsg))
			}
			// Back off.
			if !l.sleep(idlePollInterval) {
				return
			}
		case <-l.stopCh:
			return
		}
	}
}

// GetTaskSnapshot returns the current task pipeline state: the most recently
// fetched tasks with their assignment information.
func (l *Loop) GetTaskSnapshot() []TaskStateEntry {
	l.tasksMu.RLock()
	tasks := make([]Task, len(l.lastTasks))
	copy(tasks, l.lastTasks)
	l.tasksMu.RUnlock()

	l.dedupMu.Lock()
	defer l.dedupMu.Unlock()

	now := time.Now()
	var entries []TaskStateEntry
	for _, task := range tasks {
		entry := TaskStateEntry{Task: task}
		if ts, ok := l.dedup[task.ID]; ok && now.Sub(ts) < l.config.DedupTimeout {
			entry.Assigned = true
			entry.AssignedAt = ts
		}

		l.tasksMu.RLock()
		if a, ok := l.taskRuns[task.ID]; ok {
			entry.RunID = a.RunID
			if !entry.Assigned {
				entry.AssignedAt = a.AssignedAt
			}
		}
		l.tasksMu.RUnlock()

		entries = append(entries, entry)
	}
	return entries
}

// pickTaskFromPhase runs the list-tasks function and picks the first open,
// non-deduped task. Returns the task ID, title, and whether a task was found.
func (l *Loop) pickTaskFromPhase() (string, string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	tasks, err := l.listTasks(ctx, l.config.ProjectDir)
	if err != nil {
		log.Printf("orchestration loop: list-tasks failed for %s: %v", l.config.ProjectDir, err)
		return "", "", false
	}

	// Cache the latest tasks for snapshot queries.
	l.tasksMu.Lock()
	l.lastTasks = tasks
	l.tasksMu.Unlock()

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
			// Untracked sentinel task: always available, skip dedup and tracking.
			return "", task.Title, true
		}
		if !task.IsOpen() {
			continue
		}
		if ts, ok := l.dedup[task.ID]; ok && now.Sub(ts) < l.config.DedupTimeout {
			continue
		}
		l.dedup[task.ID] = now
		log.Printf("orchestration loop: assigned task %s for %s", task.ID, l.config.ProjectDir)
		return task.ID, task.Title, true
	}

	return "", "", false
}

// createAttemptForTask ensures a Task record exists in the store and creates a
// new running Attempt for it. Returns the attempt ID, or "" if attempt tracking
// is not configured or the task ID is empty.
func (l *Loop) createAttemptForTask(taskID, taskTitle, projectDir string) string {
	if l.attemptStore == nil || taskID == "" {
		return ""
	}
	ctx := context.Background()

	// Ensure the task exists before creating an attempt that references it.
	if l.taskStore != nil {
		if _, err := l.taskStore.GetTask(ctx, taskID); err != nil {
			task := &domain.Task{
				ID:         taskID,
				Title:      taskTitle,
				Source:     domain.TaskSourceExternal,
				ProjectDir: projectDir,
				CreatedAt:  time.Now(),
			}
			if saveErr := l.taskStore.SaveTask(ctx, task); saveErr != nil {
				log.Printf("orchestration loop: failed to save task %s: %v", taskID, saveErr)
			}
		}
	}

	attempt := domain.NewAttempt(taskID)
	if err := l.attemptStore.SaveAttempt(ctx, attempt); err != nil {
		log.Printf("orchestration loop: failed to create attempt for task %s: %v", taskID, err)
		return ""
	}
	log.Printf("orchestration loop: created attempt %s for task %s", attempt.ID, taskID)

	if l.activityLog != nil {
		_ = l.activityLog.Append(activitylog.Entry{
			Kind:      activitylog.KindAttemptStarted,
			TaskID:    taskID,
			AttemptID: attempt.ID,
		})
	}

	return attempt.ID
}

// completeAttempt marks the attempt with the given ID as finished with the
// result derived from the run state. No-op if attempt tracking is not configured
// or the attempt ID is empty.
func (l *Loop) completeAttempt(attemptID string, state domain.RunState) {
	if l.attemptStore == nil || attemptID == "" {
		return
	}
	ctx := context.Background()
	attempt, err := l.attemptStore.GetAttempt(ctx, attemptID)
	if err != nil {
		log.Printf("orchestration loop: failed to get attempt %s for completion: %v", attemptID, err)
		return
	}
	var result domain.AttemptResult
	switch state {
	case domain.RunStateSucceeded:
		result = domain.AttemptResultSucceeded
	case domain.RunStateCancelled:
		result = domain.AttemptResultCancelled
	default:
		result = domain.AttemptResultFailed
	}
	attempt.Complete(result)
	if err := l.attemptStore.SaveAttempt(ctx, attempt); err != nil {
		log.Printf("orchestration loop: failed to complete attempt %s: %v", attemptID, err)
	}

	if l.activityLog != nil {
		_ = l.activityLog.Append(activitylog.Entry{
			Kind:      activitylog.KindAttemptEnded,
			TaskID:    attempt.TaskID,
			AttemptID: attemptID,
			State:     string(result),
		})
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

// sleep waits for the given duration or until the loop is stopped or resumed.
// Returns false if the loop was stopped.
func (l *Loop) sleep(d time.Duration) bool {
	select {
	case <-time.After(d):
		return true
	case <-l.resumeCh:
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

	// Find the most recently modified .log file.
	var latest string
	var latestMod time.Time
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".log" {
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
