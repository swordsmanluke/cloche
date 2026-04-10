package engine

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
)

const (
	DefaultStepTimeout      = 30 * time.Minute
	HumanStepDefaultTimeout = 72 * time.Hour
)

// StepExecutor executes a single step and returns the result.
type StepExecutor interface {
	Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error)
}

// HostExecutorConfigurer is optionally implemented by composite executors
// (e.g. DaemonExecutor) that wrap a host executor. The runner calls
// SetHostExecutor after constructing a fully-configured host executor so the
// composite executor delegates host steps to it.
type HostExecutorConfigurer interface {
	SetHostExecutor(StepExecutor)
}

// StepExecutorFunc adapts a function to the StepExecutor interface.
type StepExecutorFunc func(ctx context.Context, step *domain.Step) (domain.StepResult, error)

func (f StepExecutorFunc) Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	return f(ctx, step)
}

// StatusHandler receives notifications about workflow execution progress.
type StatusHandler interface {
	OnStepStart(run *domain.Run, step *domain.Step)
	OnStepComplete(run *domain.Run, step *domain.Step, result string, usage *domain.TokenUsage)
	OnRunComplete(run *domain.Run)
}

type noopStatus struct{}

func (noopStatus) OnStepStart(*domain.Run, *domain.Step)                               {}
func (noopStatus) OnStepComplete(*domain.Run, *domain.Step, string, *domain.TokenUsage) {}
func (noopStatus) OnRunComplete(*domain.Run)                                            {}

type Engine struct {
	executor         StepExecutor
	status           StatusHandler
	maxSteps         int
	defaultTimeout   time.Duration
	preloadedResults map[string]string // step_name -> result for resume mode
	startStep        string            // when non-empty, override wf.EntryStep
}

func New(executor StepExecutor) *Engine {
	return &Engine{
		executor:       executor,
		status:         noopStatus{},
		maxSteps:       1000,
		defaultTimeout: DefaultStepTimeout,
	}
}

func (e *Engine) SetStatusHandler(h StatusHandler) {
	e.status = h
}

func (e *Engine) SetMaxSteps(n int) {
	e.maxSteps = n
}

// SetDefaultTimeout sets the default timeout for steps that don't specify one.
func (e *Engine) SetDefaultTimeout(d time.Duration) {
	e.defaultTimeout = d
}

// SetStartStep overrides the workflow entry step. When set, execution begins
// at this step instead of wf.EntryStep. Used for single-step runs.
func (e *Engine) SetStartStep(step string) {
	e.startStep = step
}

// SetPreloadedResults configures the engine to skip execution of steps whose
// results are already known. When a step in this map is launched, the engine
// immediately produces the stored result without calling the executor. This is
// used for resume mode: completed steps before the resume point are replayed.
func (e *Engine) SetPreloadedResults(results map[string]string) {
	e.preloadedResults = results
}

// stepResult is sent from worker goroutines back to the main event loop.
type stepResult struct {
	stepName string
	result   string
	usage    *domain.TokenUsage
	err      error
}

// collectState tracks the satisfaction state of a single Collect clause.
type collectState struct {
	collect   *domain.Collect
	satisfied map[int]bool
	fired     bool
}

func (e *Engine) Run(ctx context.Context, wf *domain.Workflow) (*domain.Run, error) {
	if err := wf.Validate(); err != nil {
		return nil, fmt.Errorf("invalid workflow: %w", err)
	}
	for _, w := range wf.ValidateConfig() {
		log.Printf("WARNING: %s", w)
	}

	run := domain.NewRun(generateRunID(), wf.Name)
	run.Start()

	// Check context cancellation before starting.
	if err := ctx.Err(); err != nil {
		run.Complete(domain.RunStateCancelled)
		return run, fmt.Errorf("workflow cancelled: %w", err)
	}

	// Build a set of (step, result) pairs that are handled by collects,
	// so we know when a missing wire is acceptable.
	collectHandled := make(map[string]map[string]bool)
	for _, c := range wf.Collects {
		for _, cond := range c.Conditions {
			if collectHandled[cond.Step] == nil {
				collectHandled[cond.Step] = make(map[string]bool)
			}
			collectHandled[cond.Step][cond.Result] = true
		}
	}

	// Initialize collect states.
	cStates := make([]*collectState, len(wf.Collects))
	for i := range wf.Collects {
		cStates[i] = &collectState{
			collect:   &wf.Collects[i],
			satisfied: make(map[int]bool),
		}
	}

	results := make(chan stepResult, e.maxSteps)
	activeCount := 0
	stepCount := 0
	doneCount := 0
	aborted := false
	var runErr error

	// Use a mutex to protect run state from concurrent goroutine access.
	// Only the main loop should touch the Run, but we record step start before
	// launching the goroutine, so this is safe without a mutex for now.
	// The goroutines only read the step and send on the channel.

	launchStep := func(stepName string, trigger StepTrigger) error {
		stepCount++
		if stepCount > e.maxSteps {
			return fmt.Errorf("workflow exceeded maximum step count (%d)", e.maxSteps)
		}

		step, ok := wf.Steps[stepName]
		if !ok {
			return fmt.Errorf("step %q not found in workflow", stepName)
		}

		activeCount++
		run.RecordStepStart(step.Name)
		e.status.OnStepStart(run, step)

		// Resume mode: if this step has a preloaded result, replay it
		// instead of executing. This allows completed steps to be
		// skipped while preserving the wiring logic.
		if preloadedResult, ok := e.preloadedResults[stepName]; ok {
			go func(name, result string) {
				results <- stepResult{stepName: name, result: result}
			}(step.Name, preloadedResult)
			return nil
		}

		go func(s *domain.Step, t StepTrigger) {
			stepCtx := ctx
			if d := stepTimeout(s, e.defaultTimeout); d > 0 {
				var cancel context.CancelFunc
				stepCtx, cancel = context.WithTimeout(ctx, d)
				defer cancel()
			}
			stepCtx = WithStepTrigger(stepCtx, t)
			stepCtx = WithWorkflow(stepCtx, wf)
			sr, err := e.executor.Execute(stepCtx, s)
			results <- stepResult{stepName: s.Name, result: sr.Result, usage: sr.Usage, err: err}
		}(step, trigger)

		return nil
	}

	// Launch entry step (or override step for single-step runs).
	entryStep := wf.EntryStep
	if e.startStep != "" {
		entryStep = e.startStep
	}
	if err := launchStep(entryStep, StepTrigger{}); err != nil {
		run.Complete(domain.RunStateFailed)
		return run, err
	}

	// Main event loop.
	for activeCount > 0 {
		select {
		case <-ctx.Done():
			run.Complete(domain.RunStateCancelled)
			return run, fmt.Errorf("workflow cancelled: %w", ctx.Err())

		case sr := <-results:
			activeCount--

			// Step execution error.
			if sr.err != nil {
				run.RecordStepComplete(sr.stepName, "error")
				run.Complete(domain.RunStateFailed)
				e.status.OnRunComplete(run)
				return run, fmt.Errorf("step %q execution failed: %w", sr.stepName, sr.err)
			}

			// Validate result is declared in the step's Results list.
			step := wf.Steps[sr.stepName]
			if !isResultDeclared(step, sr.result) {
				run.RecordStepComplete(sr.stepName, sr.result)
				run.Complete(domain.RunStateFailed)
				e.status.OnRunComplete(run)
				return run, fmt.Errorf("step %q returned undeclared result %q", sr.stepName, sr.result)
			}

			run.RecordStepComplete(sr.stepName, sr.result)
			e.status.OnStepComplete(run, step, sr.result, sr.usage)

			// Process wiring: get next steps for this (step, result) pair.
			nextSteps, wireErr := wf.NextSteps(sr.stepName, sr.result)
			if wireErr != nil {
				// No wire found. Check if any collect handles this (step, result).
				if !collectHandled[sr.stepName][sr.result] {
					// Neither wires nor collects handle this result.
					run.Complete(domain.RunStateFailed)
					e.status.OnRunComplete(run)
					return run, wireErr
				}
				// Collect handles it; no wire targets to launch.
			} else {
				// Process wire targets.
				for _, target := range nextSteps {
					switch target {
					case domain.StepDone:
						doneCount++
					case domain.StepAbort:
						aborted = true
					default:
						if err := launchStep(target, StepTrigger{PrevStep: sr.stepName, PrevResult: sr.result}); err != nil {
							run.Complete(domain.RunStateFailed)
							e.status.OnRunComplete(run)
							return run, err
						}
					}
				}
			}

			// Check and fire collect conditions.
			for _, cs := range cStates {
				if cs.fired {
					continue
				}
				for i, cond := range cs.collect.Conditions {
					if cond.Step == sr.stepName && cond.Result == sr.result {
						cs.satisfied[i] = true
					}
				}

				shouldFire := false
				switch cs.collect.Mode {
				case domain.CollectAll:
					shouldFire = len(cs.satisfied) == len(cs.collect.Conditions)
				case domain.CollectAny:
					shouldFire = len(cs.satisfied) > 0
				}

				if shouldFire {
					cs.fired = true
					target := cs.collect.To
					switch target {
					case domain.StepDone:
						doneCount++
					case domain.StepAbort:
						aborted = true
					default:
						if err := launchStep(target, StepTrigger{PrevStep: sr.stepName, PrevResult: sr.result}); err != nil {
							run.Complete(domain.RunStateFailed)
							e.status.OnRunComplete(run)
							return run, err
						}
					}
				}
			}
		}
	}

	// Determine final state.
	if aborted || runErr != nil {
		run.Complete(domain.RunStateFailed)
	} else if doneCount > 0 {
		run.Complete(domain.RunStateSucceeded)
	} else {
		run.Complete(domain.RunStateFailed)
		runErr = fmt.Errorf("workflow %q: no branches reached done", wf.Name)
	}

	e.status.OnRunComplete(run)
	return run, runErr
}

// isResultDeclared checks whether the given result is in the step's declared Results list.
func isResultDeclared(step *domain.Step, result string) bool {
	for _, r := range step.Results {
		if r == result {
			return true
		}
	}
	return false
}

var (
	runCounter int
	runMu      sync.Mutex
)

func generateRunID() string {
	runMu.Lock()
	defer runMu.Unlock()
	runCounter++
	return fmt.Sprintf("run-%d", runCounter)
}

// stepTimeout returns the timeout for a step. It checks step.Config["timeout"]
// first, then falls back to a type-specific default (human steps default to
// domain.DefaultHumanStepTimeout), then the provided global default.
func stepTimeout(step *domain.Step, defaultTimeout time.Duration) time.Duration {
	if raw, ok := step.Config["timeout"]; ok {
		if d, err := time.ParseDuration(raw); err == nil {
			return d
		}
	}
	if step.Type == domain.StepTypeHuman {
		return HumanStepDefaultTimeout
	}
	return defaultTimeout
}
