package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/ports"
)

// Ensure hostStatusHandler implements engine.StatusHandler.
var _ engine.StatusHandler = (*hostStatusHandler)(nil)

// Runner executes a host workflow by parsing host.cloche and walking the step graph.
type Runner struct {
	Dispatcher RunDispatcher
	Store      ports.RunStore
	TaskID     string   // optional task ID assigned by the daemon loop
	ExtraEnv   []string // additional KEY=VALUE env vars passed to all steps
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
// workflow from host.cloche.
func (r *Runner) runNamedWorkflow(ctx context.Context, projectDir string, workflowName string, orchRunID string) (*RunResult, error) {
	hostPath := filepath.Join(projectDir, ".cloche", "host.cloche")
	data, err := os.ReadFile(hostPath)
	if err != nil {
		return nil, fmt.Errorf("reading host.cloche: %w", err)
	}

	workflows, err := dsl.ParseAllForHost(string(data))
	if err != nil {
		return nil, fmt.Errorf("parsing host.cloche: %w", err)
	}

	wf, ok := workflows[workflowName]
	if !ok {
		return nil, fmt.Errorf("host.cloche has no workflow %q", workflowName)
	}

	// Create output directory for step outputs
	outputDir := filepath.Join(projectDir, ".cloche", orchRunID, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	log.Printf("host workflow: starting %q for project %s (run %s)", wf.Name, projectDir, orchRunID)

	// Persist the host run in the store so it appears in list/status/logs.
	hostRun := domain.NewRun(orchRunID, wf.Name)
	hostRun.ProjectDir = projectDir
	hostRun.IsHost = true
	if err := r.Store.CreateRun(ctx, hostRun); err != nil {
		return nil, fmt.Errorf("creating host run record: %w", err)
	}
	hostRun.Start()
	_ = r.Store.UpdateRun(ctx, hostRun)

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
		projectDir: projectDir,
		orchRunID:  orchRunID,
		store:      r.Store,
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
	hostRun, _ = r.Store.GetRun(ctx, orchRunID)
	if hostRun != nil {
		if runErr != nil {
			hostRun.Fail(runErr.Error())
		} else {
			hostRun.Complete(result.State)
		}
		hostRun.ActiveSteps = nil
		_ = r.Store.UpdateRun(ctx, hostRun)
	}

	if runErr != nil {
		log.Printf("host workflow: %q failed for %s: %v", wf.Name, projectDir, runErr)
		return result, runErr
	}

	log.Printf("host workflow: %q completed for %s with state %s", wf.Name, projectDir, run.State)
	return result, nil
}

// RunListTasksWorkflow executes the list-tasks workflow and returns the
// discovered tasks. When the workflow succeeds but finds no tasks, the run
// record is deleted from the store to avoid cluttering the run history with
// empty list-tasks entries.
func RunListTasksWorkflow(ctx context.Context, runner *Runner, projectDir string) ([]Task, *RunResult, error) {
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
	// When list-tasks returns no tasks, remove the run record to keep the
	// run history focused on runs where actual work was attempted.
	if len(tasks) == 0 && result.RunID != "" {
		_ = runner.Store.DeleteRun(ctx, result.RunID)
	}
	return tasks, result, nil
}

// hostStatusHandler logs host workflow step events and persists them to the store.
type hostStatusHandler struct {
	projectDir string
	orchRunID  string
	store      ports.RunStore
}

func (h *hostStatusHandler) OnStepStart(_ *domain.Run, step *domain.Step) {
	log.Printf("host workflow [%s]: step %q started", h.orchRunID, step.Name)
	if h.store != nil {
		if r, err := h.store.GetRun(context.Background(), h.orchRunID); err == nil {
			r.RecordStepStart(step.Name)
			_ = h.store.UpdateRun(context.Background(), r)
		}
	}
}

func (h *hostStatusHandler) OnStepComplete(_ *domain.Run, step *domain.Step, result string) {
	log.Printf("host workflow [%s]: step %q completed with result %q", h.orchRunID, step.Name, result)
	if h.store != nil {
		if r, err := h.store.GetRun(context.Background(), h.orchRunID); err == nil {
			r.RecordStepComplete(step.Name, result)
			_ = h.store.UpdateRun(context.Background(), r)
		}
	}
}

func (h *hostStatusHandler) OnRunComplete(run *domain.Run) {
	log.Printf("host workflow [%s]: run completed with state %s", h.orchRunID, run.State)
}
