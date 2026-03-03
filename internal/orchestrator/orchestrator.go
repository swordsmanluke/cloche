package orchestrator

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/cloche-dev/cloche/internal/config"
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
}

// Orchestrator pulls ready tasks and dispatches workflow runs, respecting
// per-project concurrency limits.
type Orchestrator struct {
	promptGen  PromptGenerator
	dispatch   RunDispatcher
	mu         sync.Mutex
	projects   map[string]*ProjectConfig // keyed by Dir
	inFlight   map[string]int            // keyed by Dir
}

// New creates an Orchestrator.
func New(promptGen PromptGenerator, dispatch RunDispatcher) *Orchestrator {
	return &Orchestrator{
		promptGen: promptGen,
		dispatch:  dispatch,
		projects:  make(map[string]*ProjectConfig),
		inFlight:  make(map[string]int),
	}
}

// Register adds a project to the orchestrator.
func (o *Orchestrator) Register(pc *ProjectConfig) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.projects[pc.Dir] = pc
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
	})
	return true
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

	dispatched := 0
	for i := 0; i < len(tasks) && dispatched < available; i++ {
		task := tasks[i]

		if err := pc.Tracker.Claim(ctx, task.ID); err != nil {
			log.Printf("orchestrator: failed to claim task %s: %v", task.ID, err)
			continue
		}

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
		o.mu.Unlock()

		log.Printf("orchestrator: dispatched run %s for task %s in %s", runID, task.ID, projectDir)
		dispatched++
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

// OnRunComplete should be called when a workflow run finishes. It decrements
// the in-flight counter and triggers a new orchestration cycle for that project.
func (o *Orchestrator) OnRunComplete(ctx context.Context, projectDir string) {
	o.mu.Lock()
	if o.inFlight[projectDir] > 0 {
		o.inFlight[projectDir]--
	}
	o.mu.Unlock()

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
