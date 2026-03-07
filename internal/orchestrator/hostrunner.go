package orchestrator

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloche-dev/cloche/internal/domain"
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

// RunWorkflow walks the workflow graph starting from wf.EntryStep, executing
// each step on the host. Returns "done" or "abort" as the final result.
func (r *HostRunner) RunWorkflow(ctx context.Context, wf *domain.Workflow, task ports.TrackerTask, orchRunID string) (string, error) {
	outputDir := filepath.Join(r.ProjectDir, ".cloche", orchRunID, "orchestrate")
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return domain.StepAbort, fmt.Errorf("creating output dir: %w", err)
	}

	currentStep := wf.EntryStep
	prevOutput := ""

	for {
		if currentStep == domain.StepDone || currentStep == domain.StepAbort {
			return currentStep, nil
		}

		step, ok := wf.Steps[currentStep]
		if !ok {
			return domain.StepAbort, fmt.Errorf("step %q not found in workflow", currentStep)
		}

		stepOutputPath := filepath.Join(outputDir, step.Name+".out")

		var result string
		var err error

		switch step.Type {
		case domain.StepTypeScript, domain.StepTypeAgent:
			result, err = r.runCommandStep(ctx, step, task, stepOutputPath, prevOutput)
		case domain.StepTypeWorkflow:
			result, err = r.runWorkflowStep(ctx, step, task, stepOutputPath, outputDir, prevOutput)
		default:
			return domain.StepAbort, fmt.Errorf("unsupported step type %q for step %q", step.Type, step.Name)
		}

		if err != nil {
			log.Printf("hostrunner: step %q error: %v", step.Name, err)
			result = "fail"
		}

		prevOutput = stepOutputPath

		nextStep, err := wf.NextStep(currentStep, result)
		if err != nil {
			return domain.StepAbort, fmt.Errorf("wiring error after step %q result %q: %w", currentStep, result, err)
		}
		currentStep = nextStep
	}
}

// runCommandStep executes a script or agent step via shell command.
func (r *HostRunner) runCommandStep(ctx context.Context, step *domain.Step, task ports.TrackerTask, stepOutputPath, prevOutput string) (string, error) {
	cmdStr := step.Config["run"]
	if cmdStr == "" {
		cmdStr = step.Config["prompt"]
	}
	if cmdStr == "" {
		return "fail", fmt.Errorf("step %q has no run or prompt command", step.Name)
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = r.ProjectDir
	cmd.Env = append(os.Environ(),
		"CLOCHE_TASK_ID="+task.ID,
		"CLOCHE_TASK_TITLE="+task.Title,
		"CLOCHE_TASK_BODY="+task.Description,
		"CLOCHE_PROJECT_DIR="+r.ProjectDir,
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
// It writes the dispatched run ID to stepOutputPath so downstream steps can
// reference the run's branch (cloche/<runID>).
func (r *HostRunner) runWorkflowStep(ctx context.Context, step *domain.Step, task ports.TrackerTask, stepOutputPath, outputDir, prevOutput string) (string, error) {
	workflowName := step.Config["workflow_name"]
	if workflowName == "" {
		return "fail", fmt.Errorf("step %q missing workflow_name", step.Name)
	}

	// Determine prompt source: prompt_step override or previous step output
	promptSource := prevOutput
	if ps, ok := step.Config["prompt_step"]; ok && ps != "" {
		promptSource = filepath.Join(outputDir, ps+".out")
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
	runID, err := r.Dispatch(ctx, workflowName, r.ProjectDir, prompt)
	if err != nil {
		return "fail", fmt.Errorf("dispatching workflow %q: %w", workflowName, err)
	}

	// Write the run ID to the step output so downstream steps can reference it
	if writeErr := os.WriteFile(stepOutputPath, []byte(runID+"\n"), 0644); writeErr != nil {
		log.Printf("hostrunner: failed to write run ID for %q: %v", step.Name, writeErr)
	}

	// Wait for the run to complete
	state, err := r.WaitRun.WaitRun(ctx, runID)
	if err != nil {
		return "fail", fmt.Errorf("waiting for run %s: %w", runID, err)
	}

	if state == domain.RunStateSucceeded {
		return "success", nil
	}
	return "fail", nil
}
