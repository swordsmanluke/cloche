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

// RunNamed executes a named host workflow, generating a new run ID.
func (r *Runner) RunNamed(ctx context.Context, projectDir string, workflowName string) (*RunResult, error) {
	orchRunID := domain.GenerateRunID(workflowName)
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

	// Create a logstream.Writer so host runs persist logs to full.log,
	// matching container-side behavior where the agent writes full.log.
	var ulog *logstream.Writer
	if !r.SkipRunRecord {
		runDir := filepath.Join(projectDir, ".cloche", orchRunID)
		w, err := logstream.New(runDir)
		if err != nil {
			log.Printf("host workflow [%s]: failed to create log writer: %v", orchRunID, err)
		} else {
			ulog = w
		}
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
	})

	run, runErr := eng.Run(ctx, wf)

	if ulog != nil {
		ulog.Close()
	}

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

	outputDir := filepath.Join(run.ProjectDir, ".cloche", run.ID, "output")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return nil, fmt.Errorf("creating output dir: %w", err)
	}

	log.Printf("host workflow: resuming %q from step %q for project %s (run %s)", wf.Name, resumeFrom, run.ProjectDir, run.ID)

	// Reset run state
	run.State = domain.RunStateRunning
	run.ErrorMessage = ""
	run.CompletedAt = time.Time{}
	run.ActiveSteps = nil
	if err := r.Store.UpdateRun(ctx, run); err != nil {
		return nil, fmt.Errorf("updating run state: %w", err)
	}

	executor := &Executor{
		ProjectDir: run.ProjectDir,
		MainDir:    MainWorktreeDir(run.ProjectDir),
		Dispatcher: r.Dispatcher,
		Store:      r.Store,
		OutputDir:  outputDir,
		Wires:      wf.Wiring,
		HostRunID:  run.ID,
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

	// Build preloaded results from previously completed steps
	preloaded := buildPreloadedResults(run, wf, resumeFrom)

	// Configure the executor to know which step is being resumed (for prompt resume)
	executor.ResumeStep = resumeFrom

	var ulog *logstream.Writer
	runDir := filepath.Join(run.ProjectDir, ".cloche", run.ID)
	w, err := logstream.New(runDir)
	if err != nil {
		log.Printf("host workflow [%s]: failed to create log writer: %v", run.ID, err)
	} else {
		ulog = w
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

	// Persist final state
	hostRun, _ := r.Store.GetRun(ctx, run.ID)
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
		log.Printf("host workflow: resume of %q failed for %s: %v", wf.Name, run.ProjectDir, runErr)
		return result, runErr
	}

	log.Printf("host workflow: resume of %q completed for %s with state %s", wf.Name, run.ProjectDir, engRun.State)
	return result, nil
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
			wf.ResolveAgents()
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
				wf.ResolveAgents()
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
	logWriter    *logstream.Writer // persists log entries to full.log
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
	// Read step output for logging/broadcasting.
	var stepOutput string
	outputPath := filepath.Join(h.outputDir, step.Name+".out")
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
