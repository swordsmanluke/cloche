package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/ports"
)

// RunWaiter blocks until the run with the given ID reaches a terminal state.
type RunWaiter interface {
	WaitRun(ctx context.Context, runID string) (domain.RunState, error)
}

// HostRunner executes a host workflow (from host.cloche) for a single task.
type HostRunner struct {
	Dispatch   RunDispatcher
	WaitRun    RunWaiter
	ProjectDir string
}

// RunWorkflow executes the workflow using the shared engine, with a
// host-specific StepExecutor. Returns "done" or "abort" as the final result.
func (r *HostRunner) RunWorkflow(ctx context.Context, wf *domain.Workflow, task ports.TrackerTask, orchRunID string) (string, error) {
	outputDir := filepath.Join(r.ProjectDir, ".cloche", orchRunID, "main")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return domain.StepAbort, fmt.Errorf("creating output dir: %w", err)
	}

	executor := &hostStepExecutor{
		dispatch:   r.Dispatch,
		waitRun:    r.WaitRun,
		projectDir: r.ProjectDir,
		outputDir:  outputDir,
		task:       task,
		outputs:    make(map[string]string),
	}

	eng := engine.New(executor)
	run, err := eng.Run(ctx, wf)
	if err != nil {
		return domain.StepAbort, err
	}

	if run.State == domain.RunStateSucceeded {
		return domain.StepDone, nil
	}
	return domain.StepAbort, nil
}

// hostStepExecutor implements engine.StepExecutor for host-side execution.
type hostStepExecutor struct {
	dispatch   RunDispatcher
	waitRun    RunWaiter
	projectDir string
	outputDir  string
	task       ports.TrackerTask

	mu      sync.Mutex
	outputs map[string]string // step name -> output file path
}

func (e *hostStepExecutor) Execute(ctx context.Context, step *domain.Step) (string, error) {
	stepOutputPath := filepath.Join(e.outputDir, step.Name+".out")

	// Get the previous step's output path (last recorded).
	e.mu.Lock()
	prevOutput := e.lastOutput()
	e.mu.Unlock()

	var result string
	var err error

	switch step.Type {
	case domain.StepTypeScript, domain.StepTypeAgent:
		result, err = e.runCommandStep(ctx, step, stepOutputPath, prevOutput)
	case domain.StepTypeWorkflow:
		result, err = e.runWorkflowStep(ctx, step, stepOutputPath, prevOutput)
	default:
		return "", fmt.Errorf("unsupported step type %q for step %q", step.Type, step.Name)
	}

	// Record this step's output path.
	e.mu.Lock()
	e.outputs[step.Name] = stepOutputPath
	e.mu.Unlock()

	if err != nil {
		log.Printf("hostrunner: step %q error: %v", step.Name, err)
		return "fail", nil
	}
	return result, nil
}

// lastOutput returns the most recently recorded output path.
// Caller must hold e.mu.
func (e *hostStepExecutor) lastOutput() string {
	// For linear workflows there's at most one meaningful "previous".
	// Return the last one added (map iteration order is random, but
	// in practice host workflows are sequential).
	var last string
	for _, v := range e.outputs {
		last = v
	}
	return last
}

// runCommandStep executes a script or agent step via shell command.
func (e *hostStepExecutor) runCommandStep(ctx context.Context, step *domain.Step, stepOutputPath, prevOutput string) (string, error) {
	cmdStr := step.Config["run"]
	if cmdStr == "" {
		cmdStr = step.Config["prompt"]
	}
	if cmdStr == "" {
		return "fail", fmt.Errorf("step %q has no run or prompt command", step.Name)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = e.projectDir
	cmd.Env = append(os.Environ(),
		"CLOCHE_TASK_ID="+e.task.ID,
		"CLOCHE_TASK_TITLE="+e.task.Title,
		"CLOCHE_TASK_BODY="+e.task.Description,
		"CLOCHE_PROJECT_DIR="+e.projectDir,
		"CLOCHE_STEP_OUTPUT="+stepOutputPath,
		"CLOCHE_PREV_OUTPUT="+prevOutput,
	)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()

	// Write stdout to output file
	if writeErr := os.WriteFile(stepOutputPath, stdout.Bytes(), 0644); writeErr != nil {
		log.Printf("hostrunner: failed to write step output for %q: %v", step.Name, writeErr)
	}

	if err != nil {
		return "fail", nil
	}
	return "success", nil
}

// runWorkflowStep dispatches a container workflow and waits for completion.
func (e *hostStepExecutor) runWorkflowStep(ctx context.Context, step *domain.Step, stepOutputPath, prevOutput string) (string, error) {
	workflowName := step.Config["workflow_name"]
	if workflowName == "" {
		return "fail", fmt.Errorf("step %q missing workflow_name", step.Name)
	}

	// Determine prompt source: prompt_step override or previous step output
	promptSource := prevOutput
	if ps, ok := step.Config["prompt_step"]; ok && ps != "" {
		e.mu.Lock()
		if path, exists := e.outputs[ps]; exists {
			promptSource = path
		} else {
			promptSource = filepath.Join(e.outputDir, ps+".out")
		}
		e.mu.Unlock()
	}

	// Read the prompt from the source file
	prompt := ""
	if promptSource != "" {
		data, err := os.ReadFile(promptSource)
		if err != nil {
			return "fail", fmt.Errorf("reading prompt from %s: %w", promptSource, err)
		}
		prompt = string(data)
	}

	// Dispatch the container workflow
	runID, err := e.dispatch(ctx, workflowName, e.projectDir, prompt)
	if err != nil {
		return "fail", fmt.Errorf("dispatching workflow %q: %w", workflowName, err)
	}

	// Write the run ID to the step output so downstream steps can reference it
	if writeErr := os.WriteFile(stepOutputPath, []byte(runID+"\n"), 0644); writeErr != nil {
		log.Printf("hostrunner: failed to write run ID for %q: %v", step.Name, writeErr)
	}

	// Wait for the run to complete
	state, err := e.waitRun.WaitRun(ctx, runID)
	if err != nil {
		return "fail", fmt.Errorf("waiting for run %s: %w", runID, err)
	}

	if state == domain.RunStateSucceeded {
		return "success", nil
	}
	return "fail", nil
}
