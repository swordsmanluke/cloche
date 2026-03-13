package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/ports"
)

// Ensure hostStatusHandler implements engine.StatusHandler.
var _ engine.StatusHandler = (*hostStatusHandler)(nil)

// Runner executes a host workflow by parsing host.cloche and walking the step graph.
type Runner struct {
	Dispatcher    RunDispatcher
	Store         ports.RunStore
	Captures      ports.CaptureStore       // optional: saves step captures for cloche logs
	LogBroadcast  *logstream.Broadcaster   // optional: publishes live log lines
	TaskID        string                   // optional task ID assigned by the daemon loop
	ExtraEnv      []string                 // additional KEY=VALUE env vars passed to all steps
	SkipRunRecord bool                     // when true, don't persist a run record to the store
}

// RunResult contains the outcome of a host workflow execution.
type RunResult struct {
	RunID     string
	State     domain.RunState
	OutputDir string // path to the step output directory
}

// Run parses .cloche/host.cloche from projectDir and executes the "main" workflow.
func (r *Runner) Run(ctx context.Context, projectDir string) (*RunResult, error) {
	return r.RunWithID(ctx, projectDir, domain.GenerateRunID("main"))
}

// RunWithID is like Run but uses the provided run ID instead of generating one.
func (r *Runner) RunWithID(ctx context.Context, projectDir string, orchRunID string) (*RunResult, error) {
	return r.runNamedWorkflow(ctx, projectDir, "main", orchRunID)
}

// RunNamed parses .cloche/host.cloche from projectDir and executes the workflow
// with the given name. The host.cloche file may contain multiple workflows (e.g.
// "list-tasks", "main", "finalize"). Returns an error if the named workflow is
// not found.
func (r *Runner) RunNamed(ctx context.Context, projectDir string, workflowName string) (*RunResult, error) {
	orchRunID := domain.GenerateRunID(workflowName)
	return r.runNamedWorkflow(ctx, projectDir, workflowName, orchRunID)
}

// runNamedWorkflow is the internal implementation that runs a specific named
// host workflow. Searches all .cloche files for the workflow.
func (r *Runner) runNamedWorkflow(ctx context.Context, projectDir string, workflowName string, orchRunID string) (*RunResult, error) {
	wf, err := findHostWorkflow(projectDir, workflowName)
	if err != nil {
		return nil, err
	}

	// Create output directory for step outputs
	outputDir := filepath.Join(projectDir, ".cloche", orchRunID, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	log.Printf("host workflow: starting %q for project %s (run %s)", wf.Name, projectDir, orchRunID)

	// Persist the host run in the store so it appears in list/status/logs.
	// When SkipRunRecord is set (e.g. for list-tasks polling), skip creating
	// the record to avoid cluttering the run history.
	if !r.SkipRunRecord {
		hostRun := domain.NewRun(orchRunID, wf.Name)
		hostRun.ProjectDir = projectDir
		hostRun.IsHost = true
		hostRun.TaskID = r.TaskID
		if err := r.Store.CreateRun(ctx, hostRun); err != nil {
			return nil, fmt.Errorf("creating host run record: %w", err)
		}
		hostRun.Start()
		_ = r.Store.UpdateRun(ctx, hostRun)
	}

	executor := &Executor{
		ProjectDir: projectDir,
		MainDir:    MainWorktreeDir(projectDir),
		Dispatcher: r.Dispatcher,
		Store:      r.Store,
		OutputDir:  outputDir,
		Wires:      wf.Wiring,
		HostRunID:  orchRunID,
		TaskID:     r.TaskID,
		ExtraEnv:   r.ExtraEnv,
	}

	// Configure agent from workflow-level host config
	if cmd := wf.Config["host.agent_command"]; cmd != "" {
		executor.AgentCommands = prompt.ParseCommands(cmd)
	}
	if args := wf.Config["host.agent_args"]; args != "" {
		executor.AgentArgs = strings.Fields(args)
	}

	eng := engine.New(executor)
	eng.SetStatusHandler(&hostStatusHandler{
		projectDir:   projectDir,
		orchRunID:    orchRunID,
		store:        r.Store,
		captures:     r.Captures,
		logBroadcast: r.LogBroadcast,
		outputDir:    outputDir,
	})

	run, runErr := eng.Run(ctx, wf)

	result := &RunResult{
		RunID:     orchRunID,
		State:     domain.RunStateFailed,
		OutputDir: outputDir,
	}
	if run != nil {
		result.State = run.State
	}

	// Persist final state.
	if !r.SkipRunRecord {
		hostRun, _ := r.Store.GetRun(ctx, orchRunID)
		if hostRun != nil {
			if runErr != nil {
				hostRun.Fail(runErr.Error())
			} else {
				hostRun.Complete(result.State)
			}
			hostRun.ActiveSteps = nil
			_ = r.Store.UpdateRun(ctx, hostRun)
		}
	}

	if runErr != nil {
		log.Printf("host workflow: %q failed for %s: %v", wf.Name, projectDir, runErr)
		return result, runErr
	}

	log.Printf("host workflow: %q completed for %s with state %s", wf.Name, projectDir, run.State)
	return result, nil
}

// RunListTasksWorkflow executes the list-tasks workflow and returns the
// discovered tasks. No run record is created for list-tasks executions since
// they are polling operations that would otherwise clutter the run history.
func RunListTasksWorkflow(ctx context.Context, runner *Runner, projectDir string) ([]Task, *RunResult, error) {
	runner.SkipRunRecord = true
	result, err := runner.RunNamed(ctx, projectDir, "list-tasks")
	if err != nil {
		return nil, nil, err
	}
	if result.State != domain.RunStateSucceeded {
		return nil, result, fmt.Errorf("list-tasks workflow failed with state %s", result.State)
	}
	tasks, err := ReadListTasksOutput(result.OutputDir)
	if err != nil {
		return nil, result, err
	}
	return tasks, result, nil
}

// findHostWorkflow searches all .cloche files in a project for a host workflow
// with the given name. A workflow is a host workflow if it contains a "host { }"
// block in its definition.
func findHostWorkflow(projectDir, workflowName string) (*domain.Workflow, error) {
	clocheDir := filepath.Join(projectDir, ".cloche")
	entries, _ := filepath.Glob(filepath.Join(clocheDir, "*.cloche"))

	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		workflows, err := dsl.ParseAll(string(data))
		if err != nil {
			continue
		}
		if wf, ok := workflows[workflowName]; ok && wf.Location == domain.LocationHost {
			return wf, nil
		}
	}

	return nil, fmt.Errorf("host workflow %q not found in any .cloche file", workflowName)
}

// FindHostWorkflows returns all host workflows across all .cloche files.
func FindHostWorkflows(projectDir string) (map[string]*domain.Workflow, error) {
	clocheDir := filepath.Join(projectDir, ".cloche")
	entries, err := filepath.Glob(filepath.Join(clocheDir, "*.cloche"))
	if err != nil {
		return nil, err
	}

	all := make(map[string]*domain.Workflow)
	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		workflows, err := dsl.ParseAll(string(data))
		if err != nil {
			continue
		}
		for name, wf := range workflows {
			if wf.Location == domain.LocationHost {
				all[name] = wf
			}
		}
	}

	return all, nil
}

// hostStatusHandler logs host workflow step events and persists them to the store.
// It mirrors the container-side status pipeline: saving captures and publishing
// to the log broadcaster so that cloche logs works identically for host runs.
type hostStatusHandler struct {
	projectDir   string
	orchRunID    string
	store        ports.RunStore
	captures     ports.CaptureStore
	logBroadcast *logstream.Broadcaster
	outputDir    string
}

func (h *hostStatusHandler) OnStepStart(_ *domain.Run, step *domain.Step) {
	now := time.Now()
	log.Printf("host workflow [%s]: step %q started", h.orchRunID, step.Name)
	if h.store != nil {
		if r, err := h.store.GetRun(context.Background(), h.orchRunID); err == nil {
			r.RecordStepStart(step.Name)
			_ = h.store.UpdateRun(context.Background(), r)
		}
	}
	if h.captures != nil {
		_ = h.captures.SaveCapture(context.Background(), h.orchRunID, &domain.StepExecution{
			StepName:  step.Name,
			StartedAt: now,
		})
	}
	if h.logBroadcast != nil {
		h.logBroadcast.Publish(h.orchRunID, logstream.LogLine{
			Timestamp: now.Format(time.RFC3339),
			Type:      "status",
			Content:   "step_started: " + step.Name,
			StepName:  step.Name,
		})
	}
}

func (h *hostStatusHandler) OnStepComplete(_ *domain.Run, step *domain.Step, result string) {
	now := time.Now()
	log.Printf("host workflow [%s]: step %q completed with result %q", h.orchRunID, step.Name, result)
	if h.store != nil {
		if r, err := h.store.GetRun(context.Background(), h.orchRunID); err == nil {
			r.RecordStepComplete(step.Name, result)
			_ = h.store.UpdateRun(context.Background(), r)
		}
	}
	if h.captures != nil {
		_ = h.captures.SaveCapture(context.Background(), h.orchRunID, &domain.StepExecution{
			StepName:    step.Name,
			Result:      result,
			CompletedAt: now,
		})
	}
	if h.logBroadcast != nil {
		// Read step output and publish it before the completion event.
		outputPath := filepath.Join(h.outputDir, step.Name+".out")
		if data, err := os.ReadFile(outputPath); err == nil && len(data) > 0 {
			h.logBroadcast.Publish(h.orchRunID, logstream.LogLine{
				Timestamp: now.Format(time.RFC3339),
				Type:      "script",
				Content:   string(data),
				StepName:  step.Name,
			})
		}
		h.logBroadcast.Publish(h.orchRunID, logstream.LogLine{
			Timestamp: now.Format(time.RFC3339),
			Type:      "status",
			Content:   "step_completed: " + step.Name + " -> " + result,
			StepName:  step.Name,
		})
	}
}

func (h *hostStatusHandler) OnRunComplete(run *domain.Run) {
	log.Printf("host workflow [%s]: run completed with state %s", h.orchRunID, run.State)
}
