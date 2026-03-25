package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
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
	ActivityLog   *activitylog.Logger      // optional: records step events to .cloche/activity.log
	TaskID        string                   // optional task ID assigned by the daemon loop
	TaskTitle     string                   // optional task title for display in the web UI
	AttemptID     string                   // optional attempt ID for v2 tracking
	ParentRunID   string                   // optional parent run ID (links this run to another in the UI)
	ExtraEnv       []string                 // additional KEY=VALUE env vars passed to all steps
	SkipRunRecord  bool                     // when true, don't persist a run record to the store
}

// RunResult contains the outcome of a host workflow execution.
type RunResult struct {
	RunID        string
	State        domain.RunState
	OutputDir    string // path to the step output directory
	tmpOutputDir string // non-empty if OutputDir is a temp dir that callers should clean up
}

// Run parses .cloche/host.cloche from projectDir and executes the "main" workflow.
func (r *Runner) Run(ctx context.Context, projectDir string) (*RunResult, error) {
	return r.RunWithID(ctx, projectDir, domain.GenerateRunID("main", r.AttemptID))
}

// RunWithID is like Run but uses the provided run ID instead of generating one.
func (r *Runner) RunWithID(ctx context.Context, projectDir string, orchRunID string) (*RunResult, error) {
	return r.runNamedWorkflow(ctx, projectDir, "main", orchRunID)
}

// RunNamed executes a named host workflow, generating a new run ID.
func (r *Runner) RunNamed(ctx context.Context, projectDir string, workflowName string) (*RunResult, error) {
	orchRunID := domain.GenerateRunID(workflowName, r.AttemptID)
	return r.runNamedWorkflow(ctx, projectDir, workflowName, orchRunID)
}

// RunNamedWithID executes a named host workflow using the provided run ID.
func (r *Runner) RunNamedWithID(ctx context.Context, projectDir string, workflowName string, runID string) (*RunResult, error) {
	return r.runNamedWorkflow(ctx, projectDir, workflowName, runID)
}

// runNamedWorkflow is the internal implementation that runs a specific named
// host workflow. Searches all .cloche files for the workflow.
func (r *Runner) runNamedWorkflow(ctx context.Context, projectDir string, workflowName string, orchRunID string) (*RunResult, error) {
	wf, err := findHostWorkflow(projectDir, workflowName)
	if err != nil {
		return nil, err
	}

	// Create output directory for step outputs.
	// v2 runs use .cloche/logs/<taskID>/<attemptID>/; ephemeral runs
	// without a task ID (e.g. list-tasks) use a temp directory to avoid
	// leaving v1-style dirs in the project.
	var outputDir string
	var tmpOutputDir string
	if r.TaskID != "" {
		attemptID := r.AttemptID
		if attemptID == "" {
			attemptID = domain.GenerateAttemptID()
		}
		outputDir = filepath.Join(projectDir, ".cloche", "logs", r.TaskID, attemptID)
	} else {
		dir, err := os.MkdirTemp("", "cloche-run-*")
		if err != nil {
			return nil, fmt.Errorf("creating temp output dir: %w", err)
		}
		outputDir = dir
		tmpOutputDir = dir
	}
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
		hostRun.TaskTitle = r.TaskTitle
		hostRun.AttemptID = r.AttemptID
		hostRun.ParentRunID = r.ParentRunID
		if err := r.Store.CreateRun(ctx, hostRun); err != nil {
			return nil, fmt.Errorf("creating host run record: %w", err)
		}
		hostRun.Start()
		_ = r.Store.UpdateRun(ctx, hostRun)

		// Persist ExtraEnv so resume can restore it (e.g. CLOCHE_MAIN_RUN_ID).
		saveExtraEnv(ctx, r.Store, r.TaskID, r.AttemptID, r.ExtraEnv)
	}

	executor := &Executor{
		ProjectDir:   projectDir,
		MainDir:      MainWorktreeDir(projectDir),
		Dispatcher:   r.Dispatcher,
		Store:        r.Store,
		OutputDir:    outputDir,
		Wires:        wf.Wiring,
		HostRunID:    orchRunID,
		TaskID:       r.TaskID,
		AttemptID:    r.AttemptID,
		WorkflowName: wf.Name,
		ExtraEnv:     r.ExtraEnv,
	}

	// Configure agent from workflow-level host config
	if cmd := wf.Config["host.agent_command"]; cmd != "" {
		executor.AgentCommands = prompt.ParseCommands(cmd)
	}
	if args := wf.Config["host.agent_args"]; args != "" {
		executor.AgentArgs = strings.Fields(args)
	}

	// Create a logstream.Writer so host runs persist logs to full.log,
	// matching container-side behavior where the agent writes full.log.
	var ulog *logstream.Writer
	if !r.SkipRunRecord {
		w, err := logstream.NewAtDir(outputDir)
		if err != nil {
			log.Printf("host workflow [%s]: failed to create log writer: %v", orchRunID, err)
		} else {
			ulog = w
		}
	}

	// Register run in broadcaster so IsActive returns true for live-stream callers.
	if r.LogBroadcast != nil {
		r.LogBroadcast.Start(orchRunID)
	}

	eng := engine.New(executor)
	eng.SetStatusHandler(&hostStatusHandler{
		projectDir:   projectDir,
		orchRunID:    orchRunID,
		store:        r.Store,
		captures:     r.Captures,
		logBroadcast: r.LogBroadcast,
		outputDir:    outputDir,
		logWriter:    ulog,
		activityLog:  r.ActivityLog,
		taskID:       r.TaskID,
		attemptID:    r.AttemptID,
		workflowName: wf.Name,
	})

	run, runErr := eng.Run(ctx, wf)

	if ulog != nil {
		ulog.Close()
	}

	result := &RunResult{
		RunID:        orchRunID,
		State:        domain.RunStateFailed,
		OutputDir:    outputDir,
		tmpOutputDir: tmpOutputDir,
	}
	if run != nil {
		result.State = run.State
	}

	// Persist final state. Use context.Background() because ctx may have been
	// cancelled if the run was stopped externally (cloche stop). Skip
	// overwriting a Cancelled state set by StopRun.
	if !r.SkipRunRecord {
		cleanupCtx := context.Background()
		hostRun, _ := r.Store.GetRun(cleanupCtx, orchRunID)
		if hostRun != nil {
			if hostRun.State != domain.RunStateCancelled {
				if runErr != nil {
					hostRun.Fail(runErr.Error())
				} else {
					hostRun.Complete(result.State)
				}
			}
			hostRun.ActiveSteps = nil
			_ = r.Store.UpdateRun(cleanupCtx, hostRun)
		}
	}

	// Signal live-stream subscribers that this run is done (after state update
	// so subscribers see the final state when their channel closes).
	if r.LogBroadcast != nil {
		r.LogBroadcast.Finish(orchRunID)
	}

	if runErr != nil {
		log.Printf("host workflow: %q failed for %s: %v", wf.Name, projectDir, runErr)
		return result, runErr
	}

	// Clean up ephemeral runtime state on success.
	// On failure, keep it so resume can restore ExtraEnv and context.
	if r.TaskID != "" && result.State == domain.RunStateSucceeded {
		cleanupRunContext(context.Background(), r.Store, projectDir, r.TaskID, r.AttemptID)
	}

	log.Printf("host workflow: %q completed for %s with state %s", wf.Name, projectDir, run.State)
	return result, nil
}

// ResumeRun resumes a failed host workflow run from a specific step.
// Steps before resumeFrom are replayed from their stored results.
func (r *Runner) ResumeRun(ctx context.Context, run *domain.Run, resumeFrom string) (*RunResult, error) {
	wf, err := findHostWorkflow(run.ProjectDir, run.WorkflowName)
	if err != nil {
		return nil, err
	}

	// Validate the resume step exists
	if _, ok := wf.Steps[resumeFrom]; !ok {
		return nil, fmt.Errorf("step %q not found in workflow %q", resumeFrom, run.WorkflowName)
	}

	// Use v2 log paths when task/attempt IDs are available, matching the
	// normal run path. Fall back to legacy .cloche/<runID>/output/ otherwise.
	var outputDir string
	if run.TaskID != "" && run.AttemptID != "" {
		outputDir = filepath.Join(run.ProjectDir, ".cloche", "logs", run.TaskID, run.AttemptID)
	} else {
		outputDir = filepath.Join(run.ProjectDir, ".cloche", run.ID, "output")
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	log.Printf("host workflow: resuming %q from step %q for project %s (run %s)", wf.Name, resumeFrom, run.ProjectDir, run.ID)

	// Restore child_run_id from the DB if the context store was cleaned up.
	// The finalize phase cleans up the KV store on success, but resume needs
	// child_run_id to re-run merge/cleanup steps.
	if run.TaskID != "" {
		if _, ok, _ := r.Store.GetContextKey(ctx, run.TaskID, run.AttemptID, "child_run_id"); !ok {
			if children, err := r.Store.ListChildRuns(ctx, run.ID); err == nil {
				// Find the most recent non-host child run (the container
				// workflow). Host children (like finalize) are not what
				// the merge script needs.
				for i := len(children) - 1; i >= 0; i-- {
					if !children[i].IsHost {
						_ = r.Store.SetContextKey(ctx, run.TaskID, run.AttemptID, "child_run_id", children[i].ID)
						log.Printf("host workflow: restored child_run_id=%s from DB for resume", children[i].ID)
						break
					}
				}
			}
		}
	}

	// Restore ExtraEnv from the original run's context so that env vars like
	// CLOCHE_MAIN_RUN_ID are available to re-executed steps.
	extraEnv := r.ExtraEnv
	if len(extraEnv) == 0 {
		extraEnv = loadExtraEnv(ctx, r.Store, run.TaskID, run.AttemptID)
	}

	// Reset run state
	run.State = domain.RunStateRunning
	run.ErrorMessage = ""
	run.CompletedAt = time.Time{}
	run.ActiveSteps = nil
	if err := r.Store.UpdateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("updating run state: %w", err)
	}

	executor := &Executor{
		ProjectDir:   run.ProjectDir,
		MainDir:      MainWorktreeDir(run.ProjectDir),
		Dispatcher:   r.Dispatcher,
		Store:        r.Store,
		OutputDir:    outputDir,
		Wires:        wf.Wiring,
		HostRunID:    run.ID,
		TaskID:       r.TaskID,
		WorkflowName: wf.Name,
		ExtraEnv:     extraEnv,
	}

	// Configure agent from workflow-level host config
	if cmd := wf.Config["host.agent_command"]; cmd != "" {
		executor.AgentCommands = prompt.ParseCommands(cmd)
	}
	if args := wf.Config["host.agent_args"]; args != "" {
		executor.AgentArgs = strings.Fields(args)
	}

	// Build preloaded results from previously completed steps
	preloaded := buildPreloadedResults(run, wf, resumeFrom)

	// Configure the executor to know which step is being resumed (for prompt resume)
	executor.ResumeStep = resumeFrom

	var ulog *logstream.Writer
	w, err := logstream.NewAtDir(outputDir)
	if err != nil {
		log.Printf("host workflow [%s]: failed to create log writer: %v", run.ID, err)
	} else {
		ulog = w
	}

	// Register run in broadcaster so IsActive returns true for live-stream callers.
	if r.LogBroadcast != nil {
		r.LogBroadcast.Start(run.ID)
	}

	eng := engine.New(executor)
	eng.SetPreloadedResults(preloaded)
	eng.SetStatusHandler(&hostStatusHandler{
		projectDir:   run.ProjectDir,
		orchRunID:    run.ID,
		store:        r.Store,
		captures:     r.Captures,
		logBroadcast: r.LogBroadcast,
		outputDir:    outputDir,
		logWriter:    ulog,
		activityLog:  r.ActivityLog,
		taskID:       r.TaskID,
		attemptID:    r.AttemptID,
		workflowName: wf.Name,
	})

	engRun, runErr := eng.Run(ctx, wf)

	if ulog != nil {
		ulog.Close()
	}

	result := &RunResult{
		RunID:     run.ID,
		State:     domain.RunStateFailed,
		OutputDir: outputDir,
	}
	if engRun != nil {
		result.State = engRun.State
	}

	// Persist final state. Use context.Background() because ctx may have been
	// cancelled if the run was stopped externally (cloche stop). Skip
	// overwriting a Cancelled state set by StopRun.
	{
		cleanupCtx := context.Background()
		hostRun, _ := r.Store.GetRun(cleanupCtx, run.ID)
		if hostRun != nil {
			if hostRun.State != domain.RunStateCancelled {
				if runErr != nil {
					hostRun.Fail(runErr.Error())
				} else {
					hostRun.Complete(result.State)
				}
			}
			hostRun.ActiveSteps = nil
			_ = r.Store.UpdateRun(cleanupCtx, hostRun)
		}
	}

	// Signal live-stream subscribers that this run is done.
	if r.LogBroadcast != nil {
		r.LogBroadcast.Finish(run.ID)
	}

	if runErr != nil {
		log.Printf("host workflow: resume of %q failed for %s: %v", wf.Name, run.ProjectDir, runErr)
		return result, runErr
	}

	// Clean up ephemeral runtime state on success.
	if run.TaskID != "" && result.State == domain.RunStateSucceeded {
		cleanupRunContext(context.Background(), r.Store, run.ProjectDir, run.TaskID, run.AttemptID)
	}

	log.Printf("host workflow: resume of %q completed for %s with state %s", wf.Name, run.ProjectDir, engRun.State)
	return result, nil
}

// ResumeRunAsNewAttempt resumes a failed host workflow run by creating a new
// run record under a new attempt (r.AttemptID must be the new attempt's ID).
// Step output files for successfully completed steps are copied from the old
// attempt's output directory to the new one so subsequent steps can read them.
// The old run is left in its failed state for lineage tracing.
func (r *Runner) ResumeRunAsNewAttempt(ctx context.Context, oldRun *domain.Run, resumeFrom, newRunID string) (*RunResult, error) {
	wf, err := findHostWorkflow(oldRun.ProjectDir, oldRun.WorkflowName)
	if err != nil {
		return nil, err
	}

	if _, ok := wf.Steps[resumeFrom]; !ok {
		return nil, fmt.Errorf("step %q not found in workflow %q", resumeFrom, oldRun.WorkflowName)
	}

	// New attempt uses its own output directory.
	newOutputDir := filepath.Join(oldRun.ProjectDir, ".cloche", "logs", oldRun.TaskID, r.AttemptID)
	if err := os.MkdirAll(newOutputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	// Copy step output files for successfully completed steps from the old
	// attempt's directory so downstream steps can read their predecessor outputs.
	if oldRun.TaskID != "" && oldRun.AttemptID != "" {
		oldOutputDir := filepath.Join(oldRun.ProjectDir, ".cloche", "logs", oldRun.TaskID, oldRun.AttemptID)
		copySuccessfulStepOutputs(oldRun, wf, resumeFrom, oldOutputDir, newOutputDir)
	}

	log.Printf("host workflow: resuming %q as new attempt (prev run %s → new run %s) for project %s",
		wf.Name, oldRun.ID, newRunID, oldRun.ProjectDir)

	// Restore child_run_id from the DB if the context store was cleaned up.
	if oldRun.TaskID != "" {
		if _, ok, _ := r.Store.GetContextKey(ctx, oldRun.TaskID, oldRun.AttemptID, "child_run_id"); !ok {
			if children, err := r.Store.ListChildRuns(ctx, oldRun.ID); err == nil {
				for i := len(children) - 1; i >= 0; i-- {
					if !children[i].IsHost {
						_ = r.Store.SetContextKey(ctx, oldRun.TaskID, r.AttemptID, "child_run_id", children[i].ID)
						log.Printf("host workflow: restored child_run_id=%s from DB for resume", children[i].ID)
						break
					}
				}
			}
		}
	}

	// Restore ExtraEnv from the original run's context.
	extraEnv := r.ExtraEnv
	if len(extraEnv) == 0 {
		extraEnv = loadExtraEnv(ctx, r.Store, oldRun.TaskID, oldRun.AttemptID)
	}

	// Create new run record — the old run remains in its failed state.
	hostRun := domain.NewRun(newRunID, oldRun.WorkflowName)
	hostRun.ProjectDir = oldRun.ProjectDir
	hostRun.IsHost = true
	hostRun.TaskID = oldRun.TaskID
	hostRun.TaskTitle = oldRun.TaskTitle
	hostRun.AttemptID = r.AttemptID
	hostRun.ParentRunID = oldRun.ParentRunID
	if err := r.Store.CreateRun(ctx, hostRun); err != nil {
		return nil, fmt.Errorf("creating resume run record: %w", err)
	}
	hostRun.Start()
	_ = r.Store.UpdateRun(ctx, hostRun)

	// Persist ExtraEnv so future resumes can restore it.
	saveExtraEnv(ctx, r.Store, oldRun.TaskID, r.AttemptID, extraEnv)

	executor := &Executor{
		ProjectDir:   oldRun.ProjectDir,
		MainDir:      MainWorktreeDir(oldRun.ProjectDir),
		Dispatcher:   r.Dispatcher,
		Store:        r.Store,
		OutputDir:    newOutputDir,
		Wires:        wf.Wiring,
		HostRunID:    newRunID,
		TaskID:       r.TaskID,
		AttemptID:    r.AttemptID,
		WorkflowName: wf.Name,
		ExtraEnv:     extraEnv,
		ResumeStep:   resumeFrom,
	}

	if cmd := wf.Config["host.agent_command"]; cmd != "" {
		executor.AgentCommands = prompt.ParseCommands(cmd)
	}
	if args := wf.Config["host.agent_args"]; args != "" {
		executor.AgentArgs = strings.Fields(args)
	}

	preloaded := buildPreloadedResults(oldRun, wf, resumeFrom)

	var ulog *logstream.Writer
	w, err := logstream.NewAtDir(newOutputDir)
	if err != nil {
		log.Printf("host workflow [%s]: failed to create log writer: %v", newRunID, err)
	} else {
		ulog = w
	}

	if r.LogBroadcast != nil {
		r.LogBroadcast.Start(newRunID)
	}

	eng := engine.New(executor)
	eng.SetPreloadedResults(preloaded)
	eng.SetStatusHandler(&hostStatusHandler{
		projectDir:   oldRun.ProjectDir,
		orchRunID:    newRunID,
		store:        r.Store,
		captures:     r.Captures,
		logBroadcast: r.LogBroadcast,
		outputDir:    newOutputDir,
		logWriter:    ulog,
		activityLog:  r.ActivityLog,
		taskID:       r.TaskID,
		attemptID:    r.AttemptID,
		workflowName: wf.Name,
	})

	engRun, runErr := eng.Run(ctx, wf)

	if ulog != nil {
		ulog.Close()
	}

	result := &RunResult{
		RunID:     newRunID,
		State:     domain.RunStateFailed,
		OutputDir: newOutputDir,
	}
	if engRun != nil {
		result.State = engRun.State
	}

	// Persist final state using the new run record. Use context.Background()
	// because ctx may have been cancelled if the run was stopped externally
	// (cloche stop). Skip overwriting a Cancelled state set by StopRun.
	{
		cleanupCtx := context.Background()
		hostRunFinal, _ := r.Store.GetRun(cleanupCtx, newRunID)
		if hostRunFinal != nil {
			if hostRunFinal.State != domain.RunStateCancelled {
				if runErr != nil {
					hostRunFinal.Fail(runErr.Error())
				} else {
					hostRunFinal.Complete(result.State)
				}
			}
			hostRunFinal.ActiveSteps = nil
			_ = r.Store.UpdateRun(cleanupCtx, hostRunFinal)
		}
	}

	if r.LogBroadcast != nil {
		r.LogBroadcast.Finish(newRunID)
	}

	if runErr != nil {
		log.Printf("host workflow: resume of %q (new attempt) failed for %s: %v", wf.Name, oldRun.ProjectDir, runErr)
		return result, runErr
	}

	if oldRun.TaskID != "" && result.State == domain.RunStateSucceeded {
		cleanupRunContext(context.Background(), r.Store, oldRun.ProjectDir, oldRun.TaskID, r.AttemptID)
	}

	log.Printf("host workflow: resume of %q (new attempt) completed for %s with state %s", wf.Name, oldRun.ProjectDir, engRun.State)
	return result, nil
}

// copySuccessfulStepOutputs copies step output files for steps that completed
// successfully before the resume point from oldDir to newDir. This gives the
// new attempt access to prior step outputs without re-executing those steps.
func copySuccessfulStepOutputs(run *domain.Run, wf *domain.Workflow, resumeFrom, oldDir, newDir string) {
	preloaded := buildPreloadedResults(run, wf, resumeFrom)
	for stepName := range preloaded {
		srcPath := filepath.Join(oldDir, stepName+".log")
		data, err := os.ReadFile(srcPath)
		if err != nil {
			continue // no output file for this step (e.g. workflow steps that only write run ID)
		}
		dstPath := filepath.Join(newDir, stepName+".log")
		_ = os.WriteFile(dstPath, data, 0644)
	}
}

// buildPreloadedResults creates a map of step results for steps that completed
// before the resume point. It walks the workflow graph from the entry step,
// collecting results from the run's StepExecutions.
func buildPreloadedResults(run *domain.Run, wf *domain.Workflow, resumeFrom string) map[string]string {
	// Build step result map from the run's executions
	stepResults := make(map[string]string)
	for _, exec := range run.StepExecutions {
		if exec.Result != "" && exec.Result != "error" {
			stepResults[exec.StepName] = exec.Result
		}
	}

	preloaded := make(map[string]string)
	visited := make(map[string]bool)
	var walk func(stepName string)
	walk = func(stepName string) {
		if visited[stepName] || stepName == resumeFrom {
			return
		}
		visited[stepName] = true

		result, ok := stepResults[stepName]
		if !ok {
			return
		}
		preloaded[stepName] = result

		nextSteps, err := wf.NextSteps(stepName, result)
		if err != nil {
			return
		}
		for _, next := range nextSteps {
			if next != domain.StepDone && next != domain.StepAbort {
				walk(next)
			}
		}
	}

	walk(wf.EntryStep)
	return preloaded
}

// RunListTasksWorkflow executes the list-tasks workflow and returns the
// discovered tasks. No run record is created for list-tasks executions since
// they are polling operations that would otherwise clutter the run history.
func RunListTasksWorkflow(ctx context.Context, runner *Runner, projectDir string) ([]Task, *RunResult, error) {
	runner.SkipRunRecord = true
	result, err := runner.RunNamed(ctx, projectDir, "list-tasks")
	if result != nil && result.tmpOutputDir != "" {
		defer os.RemoveAll(result.tmpOutputDir)
	}
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

const extraEnvContextKey = "extra_env"

// saveExtraEnv persists the runner's ExtraEnv into the KV store so that
// resume can restore env vars like CLOCHE_MAIN_RUN_ID.
func saveExtraEnv(ctx context.Context, store ports.RunStore, taskID, attemptID string, env []string) {
	if len(env) == 0 || store == nil {
		return
	}
	joined := strings.Join(env, "\n")
	_ = store.SetContextKey(ctx, taskID, attemptID, extraEnvContextKey, joined)
}

// loadExtraEnv restores ExtraEnv from the KV store.
func loadExtraEnv(ctx context.Context, store ports.RunStore, taskID, attemptID string) []string {
	if store == nil {
		return nil
	}
	val, ok, err := store.GetContextKey(ctx, taskID, attemptID, extraEnvContextKey)
	if err != nil || !ok || val == "" {
		return nil
	}
	return strings.Split(val, "\n")
}

// cleanupRunContext removes all KV pairs for the attempt and deletes the
// ephemeral .cloche/runs/<taskID>/ directory (used for prompt.txt).
func cleanupRunContext(ctx context.Context, store ports.RunStore, projectDir, taskID, attemptID string) {
	if store != nil {
		_ = store.DeleteContextKeys(ctx, taskID, attemptID)
	}
	runDir := filepath.Join(projectDir, ".cloche", "runs", taskID)
	_ = os.RemoveAll(runDir)
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
			wf.ResolveAgents()
			return wf, nil
		}
	}

	return nil, fmt.Errorf("host workflow %q not found in any .cloche file", workflowName)
}

// FindAllWorkflows returns all workflows across all .cloche files, keyed by name.
// Returns an error if the same workflow name appears in more than one file.
func FindAllWorkflows(projectDir string) (map[string]*domain.Workflow, error) {
	clocheDir := filepath.Join(projectDir, ".cloche")
	entries, err := filepath.Glob(filepath.Join(clocheDir, "*.cloche"))
	if err != nil {
		return nil, err
	}

	all := make(map[string]*domain.Workflow)
	// Track which file each workflow name came from for duplicate detection.
	seenIn := make(map[string]string)

	for _, path := range entries {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		workflows, err := dsl.ParseAll(string(data))
		if err != nil {
			continue
		}
		filename := filepath.Base(path)
		for name, wf := range workflows {
			if prev, exists := seenIn[name]; exists {
				return nil, fmt.Errorf("duplicate workflow name %q: defined in both %s and %s", name, prev, filename)
			}
			seenIn[name] = filename
			wf.ResolveAgents()
			all[name] = wf
		}
	}

	return all, nil
}

// FindHostWorkflows returns all host workflows across all .cloche files.
func FindHostWorkflows(projectDir string) (map[string]*domain.Workflow, error) {
	all, err := FindAllWorkflows(projectDir)
	if err != nil {
		return nil, err
	}

	host := make(map[string]*domain.Workflow, len(all))
	for name, wf := range all {
		if wf.Location == domain.LocationHost {
			host[name] = wf
		}
	}
	return host, nil
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
	logWriter    *logstream.Writer    // persists log entries to full.log
	activityLog  *activitylog.Logger  // optional: records step events to activity.log
	taskID       string               // propagated to activity log entries
	attemptID    string               // propagated to activity log entries
	workflowName string               // propagated to activity log entries
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
	if h.logWriter != nil {
		h.logWriter.Log(logstream.TypeStatus, "step_started: "+step.Name)
	}
	if h.logBroadcast != nil {
		h.logBroadcast.Publish(h.orchRunID, logstream.LogLine{
			Timestamp: now.Format(time.RFC3339),
			Type:      "status",
			Content:   "step_started: " + step.Name,
			StepName:  step.Name,
		})
	}
	if h.activityLog != nil {
		_ = h.activityLog.Append(activitylog.Entry{
			Timestamp:    now,
			Kind:         activitylog.KindStepStarted,
			TaskID:       h.taskID,
			AttemptID:    h.attemptID,
			WorkflowName: h.workflowName,
			StepName:     step.Name,
		})
	}
}

func (h *hostStatusHandler) OnStepComplete(_ *domain.Run, step *domain.Step, result string, usage *domain.TokenUsage) {
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
			Usage:       usage,
		})
	}
	if h.activityLog != nil {
		_ = h.activityLog.Append(activitylog.Entry{
			Timestamp:    now,
			Kind:         activitylog.KindStepCompleted,
			TaskID:       h.taskID,
			AttemptID:    h.attemptID,
			WorkflowName: h.workflowName,
			StepName:     step.Name,
			Result:       result,
		})
	}
	// Read step output for logging/broadcasting.
	var stepOutput string
	outputPath := filepath.Join(h.outputDir, step.Name+".log")
	if data, err := os.ReadFile(outputPath); err == nil && len(data) > 0 {
		stepOutput = string(data)
	}
	if h.logWriter != nil {
		if stepOutput != "" {
			h.logWriter.Log(logstream.TypeScript, stepOutput)
		}
		h.logWriter.Log(logstream.TypeStatus, "step_completed: "+step.Name+" -> "+result)
	}
	if h.logBroadcast != nil {
		if stepOutput != "" {
			h.logBroadcast.Publish(h.orchRunID, logstream.LogLine{
				Timestamp: now.Format(time.RFC3339),
				Type:      "script",
				Content:   stepOutput,
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
