package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/cloche-dev/cloche/internal/protocol"
	"google.golang.org/grpc/metadata"
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
	ProjectDir    string
	MainDir       string         // main branch worktree dir; scripts execute from here
	Dispatcher    RunDispatcher
	Store         ports.RunStore
	OutputDir     string         // directory for step output files
	Wires         []domain.Wire  // workflow wiring (for output mappings)
	HostRunID     string         // ID of the parent host run (set on child runs)
	AgentCommands []string       // workflow-level agent command fallback chain
	AgentArgs     []string       // workflow-level explicit agent args (overrides defaults)
	TaskID        string         // optional task ID assigned by the daemon loop
	AttemptID     string         // optional attempt ID for v2 tracking (propagated to child runs)
	WorkflowName  string         // workflow name for run context seeding
	ExtraEnv      []string       // additional KEY=VALUE env vars for all steps
	ResumeStep    string         // step being resumed (for prompt conversation resume)

	seedOnce sync.Once // ensures SeedRunContext is called exactly once
}

// scriptDir returns the directory from which scripts should execute.
// Uses MainDir if set, otherwise falls back to ProjectDir.
func (e *Executor) scriptDir() string {
	if e.MainDir != "" {
		return e.MainDir
	}
	return e.ProjectDir
}

var _ engine.StepExecutor = (*Executor)(nil)

// Execute runs a single host workflow step.
func (e *Executor) Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	// Seed run-level context once on first use (logged but not fatal on error).
	if e.TaskID != "" && e.Store != nil {
		e.seedOnce.Do(func() {
			pairs := [][2]string{
				{"task_id", e.TaskID},
				{"attempt_id", e.AttemptID},
				{"workflow", e.WorkflowName},
				{"run_id", e.HostRunID},
			}
			for _, p := range pairs {
				if err := e.Store.SetContextKey(ctx, e.TaskID, e.AttemptID, p[0], p[1]); err != nil {
					log.Printf("host executor: seeding run context key %q: %v", p[0], err)
				}
			}
		})
	}

	// Update workflow key on every step (handles workflow transitions).
	if e.TaskID != "" && e.Store != nil {
		if err := e.Store.SetContextKey(ctx, e.TaskID, e.AttemptID, "workflow", e.WorkflowName); err != nil {
			log.Printf("host executor: updating workflow context key: %v", err)
		}
	}

	// Record the step trigger (prev_step / prev_step_exit) before dispatching.
	if e.TaskID != "" && e.Store != nil {
		trigger, _ := engine.GetStepTrigger(ctx)
		for _, kv := range [][2]string{
			{"prev_step", trigger.PrevStep},
			{"prev_step_exit", trigger.PrevResult},
		} {
			if err := e.Store.SetContextKey(ctx, e.TaskID, e.AttemptID, kv[0], kv[1]); err != nil {
				log.Printf("host executor: setting %q context for step %q: %v", kv[0], step.Name, err)
			}
		}
	}

	var result domain.StepResult
	var err error
	switch step.Type {
	case domain.StepTypeScript:
		r, e2 := e.executeScript(ctx, step)
		result, err = domain.StepResult{Result: r}, e2
	case domain.StepTypeWorkflow:
		r, e2 := e.executeWorkflow(ctx, step)
		result, err = domain.StepResult{Result: r}, e2
	case domain.StepTypeAgent:
		result, err = e.executeAgent(ctx, step)
	default:
		return domain.StepResult{}, fmt.Errorf("unsupported step type %q in host workflow", step.Type)
	}

	// Record the step result after execution.
	if e.TaskID != "" && e.Store != nil && err == nil && result.Result != "" {
		key := fmt.Sprintf("%s:%s:result", e.WorkflowName, step.Name)
		if setErr := e.Store.SetContextKey(ctx, e.TaskID, e.AttemptID, key, result.Result); setErr != nil {
			log.Printf("host executor: setting step result context for %q: %v", step.Name, setErr)
		}
	}

	return result, err
}

// executeScript runs a shell command on the host.
func (e *Executor) executeScript(ctx context.Context, step *domain.Step) (string, error) {
	cmdStr := step.Config["run"]
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
	cmd.Dir = e.scriptDir()
	// Build env from parent, filtering out CLOCHE_RUN_ID so it doesn't
	// leak from the container environment when HostRunID is not set.
	var baseEnv []string
	for _, ev := range os.Environ() {
		if !strings.HasPrefix(ev, "CLOCHE_RUN_ID=") {
			baseEnv = append(baseEnv, ev)
		}
	}
	cmd.Env = append(baseEnv,
		"CLOCHE_PROJECT_DIR="+e.ProjectDir,
		"CLOCHE_STEP_OUTPUT="+e.stepOutputPath(step.Name),
	)

	// Pass run ID for identification
	if e.HostRunID != "" {
		cmd.Env = append(cmd.Env, "CLOCHE_RUN_ID="+e.HostRunID)
	}

	// Pass daemon-assigned task and attempt IDs if available
	if e.TaskID != "" {
		cmd.Env = append(cmd.Env, "CLOCHE_TASK_ID="+e.TaskID)
	}
	if e.AttemptID != "" {
		cmd.Env = append(cmd.Env, "CLOCHE_ATTEMPT_ID="+e.AttemptID)
	}

	// Pass extra env vars (e.g. finalize phase outcome vars)
	if len(e.ExtraEnv) > 0 {
		cmd.Env = append(cmd.Env, e.ExtraEnv...)
	}

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

	// Propagate attempt ID to the child run via gRPC metadata so the server
	// can link the container run to the same attempt as the parent host run.
	dispatchCtx := ctx
	if e.AttemptID != "" {
		md := metadata.Pairs(AttemptIDMetadataKey, e.AttemptID)
		dispatchCtx = metadata.NewOutgoingContext(ctx, md)
	}

	resp, err := e.Dispatcher.RunWorkflow(dispatchCtx, &pb.RunWorkflowRequest{
		WorkflowName: workflowName,
		ProjectDir:   e.ProjectDir,
		Prompt:       promptContent,
		IssueId:      e.TaskID,
	})
	if err != nil {
		return "", fmt.Errorf("dispatching workflow %q: %w", workflowName, err)
	}

	log.Printf("host executor: dispatched container workflow %q as run %s", workflowName, resp.RunId)

	// Link child run to parent host run and store in run context
	if e.HostRunID != "" {
		if childRun, err := e.Store.GetRun(ctx, resp.RunId); err == nil {
			childRun.ParentRunID = e.HostRunID
			childRun.TaskID = e.TaskID
			_ = e.Store.UpdateRun(ctx, childRun)
		}
		// Store child run ID in context so downstream steps can retrieve it
		// via "cloche get child_run_id"
		if e.Store != nil {
			_ = e.Store.SetContextKey(ctx, e.TaskID, e.AttemptID, "child_run_id", resp.RunId)
		}
	}

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

// executeAgent runs an agent command on the host using the prompt adapter.
func (e *Executor) executeAgent(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	adapter := prompt.New()

	// Apply workflow-level agent config
	if len(e.AgentCommands) > 0 {
		adapter.Commands = e.AgentCommands
	}
	if len(e.AgentArgs) > 0 {
		adapter.ExplicitArgs = e.AgentArgs
	}

	// Step-level overrides
	if cmd := step.Config["agent_command"]; cmd != "" {
		adapter.Commands = prompt.ParseCommands(cmd)
	}
	if args := step.Config["agent_args"]; args != "" {
		adapter.ExplicitArgs = strings.Fields(args)
	}

	// Populate usage_command from config.toml [agents.codex] if the agent is codex.
	if cfg, err := config.Load(e.ProjectDir); err == nil {
		if cfg.Agents.Codex.UsageCommand != "" && isCodexCommand(adapter.Commands) {
			adapter.UsageCommand = cfg.Agents.Codex.UsageCommand
		}
	}

	adapter.RunID = e.HostRunID

	// Resume mode: if this is the step being resumed, use conversation resume
	if e.ResumeStep == step.Name {
		adapter.ResumeConversation = true
	}

	// Write previous step output as user prompt so the adapter can pick it up.
	var promptContent string
	promptSource := step.Config["prompt_step"]
	if promptSource != "" {
		if data, err := os.ReadFile(e.stepOutputPath(promptSource)); err == nil {
			promptContent = string(data)
		}
	} else if prev := e.findPrevOutput(step); prev != "" {
		if data, err := os.ReadFile(prev); err == nil {
			promptContent = string(data)
		}
	}

	if promptContent != "" {
		promptPath := filepath.Join(e.ProjectDir, ".cloche", "runs", e.TaskID, "prompt.txt")
		_ = os.MkdirAll(filepath.Dir(promptPath), 0755)
		_ = os.WriteFile(promptPath, []byte(promptContent), 0644)
	}

	sr, err := adapter.Execute(ctx, step, e.ProjectDir)
	if err != nil {
		return domain.StepResult{}, err
	}

	// Copy output from adapter's output location to executor's output path
	adapterOutput := filepath.Join(e.ProjectDir, ".cloche", "output", step.Name+".log")
	if data, readErr := os.ReadFile(adapterOutput); readErr == nil {
		if mkErr := os.MkdirAll(e.OutputDir, 0755); mkErr == nil {
			_ = os.WriteFile(e.stepOutputPath(step.Name), data, 0644)
		}
	}

	return sr, nil
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
	return filepath.Join(e.OutputDir, stepName+".log")
}

// isCodexCommand reports whether any command in the chain is "codex".
func isCodexCommand(commands []string) bool {
	for _, c := range commands {
		if strings.HasSuffix(c, "codex") || strings.Contains(c, "codex") {
			return true
		}
	}
	return false
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

// MainWorktreeDir returns the path of the main (non-linked) git worktree for
// the repository containing projectDir. If projectDir is already the main
// worktree, it returns projectDir unchanged. Falls back to projectDir on any
// error (e.g. not a git repo).
func MainWorktreeDir(projectDir string) string {
	cmd := exec.Command("git", "-C", projectDir, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return projectDir
	}
	// The first "worktree <path>" line is always the main worktree.
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "worktree ") {
			return strings.TrimPrefix(line, "worktree ")
		}
	}
	return projectDir
}
