package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
)

const (
	DefaultStepTimeout      = 30 * time.Minute
	HumanStepDefaultTimeout = 72 * time.Hour

	DefaultStepTokenLimit     int64 = 500_000
	DefaultWorkflowTokenLimit int64 = 2_000_000
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
	// OnStepSkipped is called when a step's skip script exits 0, bypassing
	// execution. wire is the result the skip script chose for routing.
	OnStepSkipped(run *domain.Run, step *domain.Step, wire string)
	OnRunComplete(run *domain.Run)
}

type noopStatus struct{}

func (noopStatus) OnStepStart(*domain.Run, *domain.Step)                               {}
func (noopStatus) OnStepComplete(*domain.Run, *domain.Step, string, *domain.TokenUsage) {}
func (noopStatus) OnStepSkipped(*domain.Run, *domain.Step, string)                     {}
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
	skipped  bool // true when the executor's skip script bypassed execution
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
	stepLaunchCounts := make(map[string]int)
	var workflowOutputTokens int64

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

		// Enforce max_attempts: when a step with max_attempts has been executed
		// that many times already (skipped invocations don't count), synthesize
		// "give-up" instead of executing again.
		if maxAttemptsStr, hasMax := step.Config["max_attempts"]; hasMax {
			if maxAttempts, parseErr := strconv.Atoi(maxAttemptsStr); parseErr == nil && maxAttempts > 0 {
				if stepLaunchCounts[stepName] >= maxAttempts {
					log.Printf("engine: step %q max_attempts (%d) exhausted, synthesizing give-up", step.Name, maxAttempts)
					activeCount++
					run.RecordStepStart(step.Name)
					e.status.OnStepStart(run, step)
					go func(name string) {
						results <- stepResult{stepName: name, result: "give-up"}
					}(step.Name)
					return nil
				}
			}
		}
		// stepLaunchCounts[stepName] is incremented in the result handler, but
		// only for non-skipped executions, so it counts real attempts only.

		// token-limit = 0: short-circuit before calling the executor.
		if stepTokenLimit(step, DefaultStepTokenLimit) == 0 {
			activeCount++
			run.RecordStepStart(step.Name)
			e.status.OnStepStart(run, step)
			go func(name string) {
				results <- stepResult{stepName: name, result: "token-limit"}
			}(step.Name)
			return nil
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

		go func(s *domain.Step, t StepTrigger, baseCtx context.Context) {
			stepCtx := baseCtx
			if d := stepTimeout(s, e.defaultTimeout); d > 0 {
				var cancel context.CancelFunc
				stepCtx, cancel = context.WithTimeout(baseCtx, d)
				defer cancel()
			}
			stepCtx = WithStepTrigger(stepCtx, t)
			stepCtx = WithWorkflow(stepCtx, wf)
			sr, err := e.executor.Execute(stepCtx, s)
			results <- stepResult{stepName: s.Name, result: sr.Result, usage: sr.Usage, err: err, skipped: sr.Skipped}
		}(step, trigger, ctx)

		return nil
	}

	// Workflow-level token-limit = 0: abort before launching any step.
	if workflowTokenLimit(wf, DefaultWorkflowTokenLimit) == 0 {
		run.Complete(domain.RunStateFailed)
		e.status.OnRunComplete(run)
		return run, nil
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
	cancelled := false
	for activeCount > 0 {
		select {
		case <-ctx.Done():
			// Switch to graceful cancellation: replace ctx with a fresh background
			// context so that cleanup steps (e.g. unclaim) can still execute.
			// Active steps will return context errors shortly; we drain them below
			// and synthesize "fail" so that fail-branch wires are walked normally.
			cancelled = true
			ctx = context.Background()

		case sr := <-results:
			activeCount--
			step := wf.Steps[sr.stepName]

			if sr.skipped {
				// Skip path: validate the chosen wire, record as skipped, do not
				// count this invocation against max_attempts.
				if !isResultDeclared(step, sr.result) {
					run.RecordStepComplete(sr.stepName, sr.result)
					run.Complete(domain.RunStateFailed)
					e.status.OnRunComplete(run)
					return run, fmt.Errorf("step %q skip script returned undeclared wire %q", sr.stepName, sr.result)
				}
				run.RecordStepSkipped(sr.stepName, sr.result)
				e.status.OnStepSkipped(run, step, sr.result)
			} else {
				// Normal execution path: increment attempt count.
				stepLaunchCounts[sr.stepName]++

				// Step execution error.
				if sr.err != nil {
					if cancelled && isContextError(sr.err) {
						// Treat cancellation as "fail" so fail-branch wires (e.g. unclaim) run.
						if isResultDeclared(step, "fail") {
							sr = stepResult{stepName: sr.stepName, result: "fail"}
							// Fall through to validate+record below.
						} else {
							// No "fail" result declared; record and drain without walking wires.
							run.RecordStepComplete(sr.stepName, "error")
							continue
						}
					} else {
						run.RecordStepComplete(sr.stepName, "error")
						run.Complete(domain.RunStateFailed)
						e.status.OnRunComplete(run)
						return run, fmt.Errorf("step %q execution failed: %w", sr.stepName, sr.err)
					}
				}

				// Validate result is declared in the step's Results list.
				if !isResultDeclared(step, sr.result) {
					run.RecordStepComplete(sr.stepName, sr.result)
					run.Complete(domain.RunStateFailed)
					e.status.OnRunComplete(run)
					return run, fmt.Errorf("step %q returned undeclared result %q", sr.stepName, sr.result)
				}

				// Step-level token-limit enforcement: override result if output tokens exceeded.
				if limit := stepTokenLimit(step, DefaultStepTokenLimit); limit != -1 && sr.usage != nil && sr.usage.OutputTokens > limit {
					sr.result = "token-limit"
				}

				run.RecordStepComplete(sr.stepName, sr.result)
				e.status.OnStepComplete(run, step, sr.result, sr.usage)

				// Workflow-level token accumulation and enforcement.
				if sr.usage != nil {
					workflowOutputTokens += sr.usage.OutputTokens
					if wfLimit := workflowTokenLimit(wf, DefaultWorkflowTokenLimit); wfLimit != -1 && workflowOutputTokens >= wfLimit {
						aborted = true
					}
				}
			}

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
	if cancelled {
		run.Complete(domain.RunStateCancelled)
		e.status.OnRunComplete(run)
		return run, fmt.Errorf("workflow cancelled: %w", context.Canceled)
	}
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

// isContextError reports whether err is (or wraps) a context cancellation or deadline.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
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

// stepTokenLimit returns the output-token limit for a step. Returns the
// configured value if present, otherwise defaultLimit. -1 means disabled.
func stepTokenLimit(step *domain.Step, defaultLimit int64) int64 {
	if raw, ok := step.Config["token-limit"]; ok {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return n
		}
	}
	return defaultLimit
}

// workflowTokenLimit returns the cumulative output-token limit for a workflow.
// Returns the configured value if present, otherwise defaultLimit. -1 means disabled.
func workflowTokenLimit(wf *domain.Workflow, defaultLimit int64) int64 {
	if raw, ok := wf.Config["token-limit"]; ok {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return n
		}
	}
	return defaultLimit
}
