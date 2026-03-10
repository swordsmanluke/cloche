package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/cloche-dev/cloche/internal/protocol"
)

// resolveOutputMappings finds all wires targeting stepName that have output mappings,
// reads the source step's output, evaluates each mapping path, and returns KEY=value
// strings suitable for adding to a command's environment.
func (e *Executor) resolveOutputMappings(stepName string, wires []domain.Wire) ([]string, error) {
	var env []string
	for _, w := range wires {
		if w.To != stepName || len(w.OutputMap) == 0 {
			continue
		}
		data, err := os.ReadFile(e.stepOutputPath(w.From))
		if err != nil {
			return nil, fmt.Errorf("reading output of %s: %w", w.From, err)
		}
		for _, m := range w.OutputMap {
			val, err := m.Path.Evaluate(data)
			if err != nil {
				return nil, fmt.Errorf("mapping %s for step %s: %w", m.EnvVar, stepName, err)
			}
			env = append(env, m.EnvVar+"="+val)
		}
	}
	return env, nil
}

// RunDispatcher dispatches a container workflow run and returns the run ID.
type RunDispatcher interface {
	RunWorkflow(ctx context.Context, req *pb.RunWorkflowRequest) (*pb.RunWorkflowResponse, error)
}

// Executor implements engine.StepExecutor for host workflow steps.
type Executor struct {
	ProjectDir string
	Dispatcher RunDispatcher
	Store      ports.RunStore
	OutputDir  string         // directory for step output files
	Wires      []domain.Wire  // workflow wiring (for output mappings)
}

var _ engine.StepExecutor = (*Executor)(nil)

// Execute runs a single host workflow step.
func (e *Executor) Execute(ctx context.Context, step *domain.Step) (string, error) {
	switch step.Type {
	case domain.StepTypeScript:
		return e.executeScript(ctx, step)
	case domain.StepTypeWorkflow:
		return e.executeWorkflow(ctx, step)
	default:
		return "", fmt.Errorf("unsupported step type %q in host workflow", step.Type)
	}
}

// executeScript runs a shell command on the host.
func (e *Executor) executeScript(ctx context.Context, step *domain.Step) (string, error) {
	cmdStr := step.Config["run"]
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = e.ProjectDir
	cmd.Env = append(os.Environ(),
		"CLOCHE_PROJECT_DIR="+e.ProjectDir,
		"CLOCHE_STEP_OUTPUT="+e.stepOutputPath(step.Name),
	)

	// Pass previous step output if available
	if prevOutput := e.findPrevOutput(step); prevOutput != "" {
		cmd.Env = append(cmd.Env, "CLOCHE_PREV_OUTPUT="+prevOutput)
	}

	// Resolve output mappings from wires and add as env vars
	if len(e.Wires) > 0 {
		mapped, err := e.resolveOutputMappings(step.Name, e.Wires)
		if err != nil {
			return "", fmt.Errorf("resolving output mappings for step %q: %w", step.Name, err)
		}
		cmd.Env = append(cmd.Env, mapped...)
	}

	output, err := cmd.CombinedOutput()

	// Extract result marker
	markerResult, cleanOutput, found := protocol.ExtractResult(output)

	// Write output to file
	if mkErr := os.MkdirAll(e.OutputDir, 0755); mkErr == nil {
		_ = os.WriteFile(e.stepOutputPath(step.Name), cleanOutput, 0644)
	}

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			result := "fail"
			if found {
				result = markerResult
			}
			return result, nil
		}
		return "", err
	}

	result := "success"
	if found {
		result = markerResult
	}
	return result, nil
}

// executeWorkflow dispatches a container workflow run and blocks until it completes.
func (e *Executor) executeWorkflow(ctx context.Context, step *domain.Step) (string, error) {
	workflowName := step.Config["workflow_name"]
	if workflowName == "" {
		return "", fmt.Errorf("workflow step %q missing workflow_name", step.Name)
	}

	// Read prompt from previous step output or prompt_step override
	var promptContent string
	promptSource := step.Config["prompt_step"]
	if promptSource != "" {
		data, err := os.ReadFile(e.stepOutputPath(promptSource))
		if err == nil {
			promptContent = string(data)
		}
	} else if prev := e.findPrevOutput(step); prev != "" {
		data, err := os.ReadFile(prev)
		if err == nil {
			promptContent = string(data)
		}
	}

	resp, err := e.Dispatcher.RunWorkflow(ctx, &pb.RunWorkflowRequest{
		WorkflowName: workflowName,
		ProjectDir:   e.ProjectDir,
		Prompt:       promptContent,
	})
	if err != nil {
		return "", fmt.Errorf("dispatching workflow %q: %w", workflowName, err)
	}

	log.Printf("host executor: dispatched container workflow %q as run %s", workflowName, resp.RunId)

	// Write run ID to step output so downstream steps (e.g. merge) can find it
	if mkErr := os.MkdirAll(e.OutputDir, 0755); mkErr == nil {
		_ = os.WriteFile(e.stepOutputPath(step.Name), []byte(resp.RunId), 0644)
	}

	// Poll until the run reaches a terminal state
	state, err := e.waitForRun(ctx, resp.RunId)
	if err != nil {
		return "", fmt.Errorf("waiting for workflow %q run %s: %w", workflowName, resp.RunId, err)
	}

	log.Printf("host executor: workflow %q run %s completed with state %s", workflowName, resp.RunId, state)

	// Map run state to step result
	if state == domain.RunStateSucceeded {
		return "success", nil
	}
	return "fail", nil
}

// waitForRun polls the store until the run reaches a terminal state.
func (e *Executor) waitForRun(ctx context.Context, runID string) (domain.RunState, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("context cancelled while waiting for run %s: %w", runID, ctx.Err())
		case <-ticker.C:
			run, err := e.Store.GetRun(ctx, runID)
			if err != nil {
				continue // transient error, retry
			}
			switch run.State {
			case domain.RunStateSucceeded, domain.RunStateFailed, domain.RunStateCancelled:
				return run.State, nil
			}
			// Still pending or running, continue polling
		}
	}
}

// stepOutputPath returns the path for a step's output file.
func (e *Executor) stepOutputPath(stepName string) string {
	return filepath.Join(e.OutputDir, stepName+".out")
}

// findPrevOutput finds the output file from the step that wires into this one.
// It checks prompt_step config first, then walks the wiring graph to find the
// source step.
func (e *Executor) findPrevOutput(step *domain.Step) string {
	// Explicit prompt_step config takes priority
	if ps := step.Config["prompt_step"]; ps != "" {
		path := e.stepOutputPath(ps)
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}

	// Walk wiring to find the step that feeds into this one
	for _, w := range e.Wires {
		if w.To == step.Name {
			path := e.stepOutputPath(w.From)
			if _, err := os.Stat(path); err == nil {
				return path
			}
		}
	}
	return ""
}
