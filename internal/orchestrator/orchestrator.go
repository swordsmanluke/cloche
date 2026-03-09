package orchestrator

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

// RunDispatcher dispatches a workflow run and returns the run ID.
type RunDispatcher func(ctx context.Context, workflowName, projectDir, prompt string) (string, error)

// ProjectConfig holds orchestration settings for a single project.
type ProjectConfig struct {
	Dir         string
	Workflow    string
	Concurrency int
	Tracker     ports.TaskTracker
	Enabled     bool
}

// ParseHostWorkflowFunc parses a host.cloche file and returns the named workflow.
type ParseHostWorkflowFunc func(input string) (*domain.Workflow, error)

// Orchestrator pulls ready tasks and dispatches workflow runs, respecting
// per-project concurrency limits.
type Orchestrator struct {
	promptGen          PromptGenerator
	dispatch           RunDispatcher
	mu                 sync.Mutex
	projects           map[string]*ProjectConfig // keyed by Dir
	inFlight           map[string]int            // keyed by Dir
	inFlightTasks      map[string]string         // runID -> taskID
	inFlightTaskSet    map[string]map[string]bool // projectDir -> set of taskIDs in flight
	hostRunner         *HostRunner
	parseHostWorkflow  ParseHostWorkflowFunc
}

// New creates an Orchestrator.
func New(promptGen PromptGenerator, dispatch RunDispatcher, opts ...OrchestratorOption) *Orchestrator {
	o := &Orchestrator{
		promptGen:       promptGen,
		dispatch:        dispatch,
		projects:        make(map[string]*ProjectConfig),
		inFlight:        make(map[string]int),
		inFlightTasks:   make(map[string]string),
		inFlightTaskSet: make(map[string]map[string]bool),
	}
	for _, opt := range opts {
		opt(o)
	}
	return o
}

// OrchestratorOption configures optional fields on the Orchestrator.
type OrchestratorOption func(*Orchestrator)

// WithHostRunner sets the HostRunner for host.cloche support.
func WithHostRunner(hr *HostRunner) OrchestratorOption {
	return func(o *Orchestrator) {
		o.hostRunner = hr
	}
}

// WithParseHostWorkflow sets the parser function for host.cloche files.
func WithParseHostWorkflow(fn ParseHostWorkflowFunc) OrchestratorOption {
	return func(o *Orchestrator) {
		o.parseHostWorkflow = fn
	}
}

// Register adds a project to the orchestrator.
func (o *Orchestrator) Register(pc *ProjectConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()
	pc.Enabled = true
	o.projects[pc.Dir] = pc
}

// Status returns the enabled flag, in-flight count, and concurrency for a project.
func (o *Orchestrator) Status(dir string) (enabled bool, inFlight int, concurrency int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	pc, ok := o.projects[dir]
	if !ok {
		return false, 0, 0
	}
	return pc.Enabled, o.inFlight[dir], pc.Concurrency
}

// SetEnabled enables or disables orchestration for a project.
func (o *Orchestrator) SetEnabled(dir string, enabled bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if pc, ok := o.projects[dir]; ok {
		pc.Enabled = enabled
	}
}

// SetConcurrency overrides the concurrency limit for a project (runtime only).
func (o *Orchestrator) SetConcurrency(dir string, concurrency int) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if pc, ok := o.projects[dir]; ok {
		pc.Concurrency = concurrency
	}
}

// RegisterFromConfig registers a project using its config file.
// Returns false if orchestration is not enabled for the project.
func (o *Orchestrator) RegisterFromConfig(projectDir string, tracker ports.TaskTracker) bool {
	cfg, err := config.Load(projectDir)
	if err != nil || !cfg.Orchestration.Enabled {
		return false
	}
	workflow := cfg.Orchestration.Workflow
	if workflow == "" {
		workflow = "develop"
	}
	concurrency := cfg.Orchestration.Concurrency
	if concurrency < 1 {
		concurrency = 1
	}
	o.Register(&ProjectConfig{
		Dir:         projectDir,
		Workflow:    workflow,
		Concurrency: concurrency,
		Tracker:     tracker,
		Enabled:     true,
	})
	return true
}

// trackTask records a run-to-task mapping for later completion/failure.
func (o *Orchestrator) trackTask(projectDir, runID, taskID string) {
	o.inFlightTasks[runID] = taskID
	if o.inFlightTaskSet[projectDir] == nil {
		o.inFlightTaskSet[projectDir] = make(map[string]bool)
	}
	o.inFlightTaskSet[projectDir][taskID] = true
}

// untrackTask removes a run-to-task mapping. Returns the task ID.
func (o *Orchestrator) untrackTask(projectDir, runID string) string {
	taskID := o.inFlightTasks[runID]
	delete(o.inFlightTasks, runID)
	if taskID != "" && o.inFlightTaskSet[projectDir] != nil {
		delete(o.inFlightTaskSet[projectDir], taskID)
	}
	return taskID
}

// isTaskInFlight checks if a task already has an active run.
func (o *Orchestrator) isTaskInFlight(projectDir, taskID string) bool {
	if set := o.inFlightTaskSet[projectDir]; set != nil {
		return set[taskID]
	}
	return false
}

// Run performs one orchestration cycle for a single project. It returns the
// number of runs dispatched.
func (o *Orchestrator) Run(ctx context.Context, projectDir string) (int, error) {
	o.mu.Lock()
	pc, ok := o.projects[projectDir]
	if !ok {
		o.mu.Unlock()
		return 0, fmt.Errorf("project %q not registered", projectDir)
	}
	if !pc.Enabled {
		o.mu.Unlock()
		return 0, nil
	}
	current := o.inFlight[projectDir]
	available := pc.Concurrency - current
	o.mu.Unlock()

	if available <= 0 {
		return 0, nil
	}

	tasks, err := pc.Tracker.ListReady(ctx, projectDir)
	if err != nil {
		return 0, fmt.Errorf("listing ready tasks: %w", err)
	}

	// Check if host.cloche exists for this project
	hostWorkflowPath := filepath.Join(projectDir, ".cloche", "host.cloche")
	useHostWorkflow := o.hostRunner != nil && o.parseHostWorkflow != nil
	if useHostWorkflow {
		if _, err := os.Stat(hostWorkflowPath); os.IsNotExist(err) {
			useHostWorkflow = false
		}
	}

	dispatched := 0
	for i := 0; i < len(tasks) && dispatched < available; i++ {
		task := tasks[i]

		// Skip tasks that already have an in-flight run (dedup)
		o.mu.Lock()
		alreadyRunning := o.isTaskInFlight(projectDir, task.ID)
		o.mu.Unlock()
		if alreadyRunning {
			log.Printf("orchestrator: skipping task %s (already in flight)", task.ID)
			continue
		}

		if err := pc.Tracker.Claim(ctx, task.ID); err != nil {
			log.Printf("orchestrator: failed to claim task %s: %v", task.ID, err)
			continue
		}

		if useHostWorkflow {
			// Host workflow path
			data, err := os.ReadFile(hostWorkflowPath)
			if err != nil {
				log.Printf("orchestrator: failed to read host.cloche for task %s: %v", task.ID, err)
				_ = pc.Tracker.Fail(ctx, task.ID)
				continue
			}

			wf, err := o.parseHostWorkflow(string(data))
			if err != nil {
				log.Printf("orchestrator: failed to parse host.cloche for task %s: %v", task.ID, err)
				_ = pc.Tracker.Fail(ctx, task.ID)
				continue
			}

			orchRunID := domain.GenerateRunID("orchestrate")

			o.mu.Lock()
			o.inFlight[projectDir]++
			o.trackTask(projectDir, orchRunID, task.ID)
			o.mu.Unlock()

			hr := &HostRunner{
				Dispatch:   o.hostRunner.Dispatch,
				WaitRun:    o.hostRunner.WaitRun,
				ProjectDir: projectDir,
			}

			go func(task ports.TrackerTask, runID string) {
				result, err := hr.RunWorkflow(context.Background(), wf, task, runID)

				o.mu.Lock()
				o.untrackTask(projectDir, runID)
				if o.inFlight[projectDir] > 0 {
					o.inFlight[projectDir]--
				}
				o.mu.Unlock()

				if err != nil {
					log.Printf("orchestrator: host workflow error for task %s: %v", task.ID, err)
					_ = pc.Tracker.Fail(ctx, task.ID)
				} else if result == domain.StepDone {
					log.Printf("orchestrator: task %s completed successfully", task.ID)
					_ = pc.Tracker.Complete(ctx, task.ID)
				} else {
					log.Printf("orchestrator: task %s aborted", task.ID)
					_ = pc.Tracker.Fail(ctx, task.ID)
				}
			}(task, orchRunID)

			log.Printf("orchestrator: started host workflow %s for task %s in %s", orchRunID, task.ID, projectDir)
			dispatched++
		} else {
			// Fallback: existing promptGen → dispatch path
			prompt, err := o.promptGen.Generate(ctx, task, projectDir)
			if err != nil {
				log.Printf("orchestrator: failed to generate prompt for task %s: %v", task.ID, err)
				_ = pc.Tracker.Fail(ctx, task.ID)
				continue
			}

			runID, err := o.dispatch(ctx, pc.Workflow, projectDir, prompt)
			if err != nil {
				log.Printf("orchestrator: failed to dispatch run for task %s: %v", task.ID, err)
				_ = pc.Tracker.Fail(ctx, task.ID)
				continue
			}

			o.mu.Lock()
			o.inFlight[projectDir]++
			o.trackTask(projectDir, runID, task.ID)
			o.mu.Unlock()

			log.Printf("orchestrator: dispatched run %s for task %s in %s", runID, task.ID, projectDir)
			dispatched++
		}
	}

	return dispatched, nil
}

// TriggerAll runs orchestration for all registered projects. Returns total
// dispatched count.
func (o *Orchestrator) TriggerAll(ctx context.Context) int {
	o.mu.Lock()
	dirs := make([]string, 0, len(o.projects))
	for dir := range o.projects {
		dirs = append(dirs, dir)
	}
	o.mu.Unlock()

	total := 0
	for _, dir := range dirs {
		n, err := o.Run(ctx, dir)
		if err != nil {
			log.Printf("orchestrator: error for project %s: %v", dir, err)
			continue
		}
		total += n
	}
	return total
}

// OnRunComplete should be called when a workflow run finishes. It looks up the
// associated task, completes or fails it in the tracker, decrements the
// in-flight counter, and triggers a new orchestration cycle.
func (o *Orchestrator) OnRunComplete(ctx context.Context, projectDir string, runID string, state domain.RunState) {
	o.mu.Lock()
	taskID := o.untrackTask(projectDir, runID)
	if taskID == "" {
		// Inner run not tracked by orchestrator (e.g. develop run inside a
		// host workflow). The host workflow goroutine manages its own
		// in-flight count and task lifecycle, so skip everything here.
		o.mu.Unlock()
		return
	}
	pc := o.projects[projectDir]
	if o.inFlight[projectDir] > 0 {
		o.inFlight[projectDir]--
	}
	o.mu.Unlock()

	// Close/fail the associated task in the tracker
	if pc != nil {
		if state == domain.RunStateSucceeded {
			log.Printf("orchestrator: completing task %s (run %s succeeded)", taskID, runID)
			_ = pc.Tracker.Complete(ctx, taskID)
		} else {
			log.Printf("orchestrator: failing task %s (run %s state: %s)", taskID, runID, state)
			_ = pc.Tracker.Fail(ctx, taskID)
		}
	}

	if _, err := o.Run(ctx, projectDir); err != nil {
		log.Printf("orchestrator: post-completion trigger failed for %s: %v", projectDir, err)
	}
}

// InFlight returns the current in-flight run count for a project.
func (o *Orchestrator) InFlight(projectDir string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.inFlight[projectDir]
}
