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

	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/cloche-dev/cloche/internal/protocol"
)

// Executor implements engine.StepExecutor for host workflow steps (scripts and
// local agents). Workflow_name step dispatch is handled by the DaemonExecutor.
type Executor struct {
	ProjectDir      string
	MainDir         string               // main branch worktree dir; scripts execute from here
	Store           ports.RunStore
	HumanPollStore  ports.HumanPollStore // optional: persists human step poll state
	PollCoord       *PollCoordinator     // optional: loop-driven poll coordinator for human steps
	OutputDir       string               // directory for step output files
	Wires           []domain.Wire        // workflow wiring (for previous-step output lookup)
	HostRunID       string               // ID of the parent host run (set on child runs)
	AgentCommands   []string             // workflow-level agent command fallback chain
	AgentArgs       []string             // workflow-level explicit agent args (overrides defaults)
	TaskID          string               // optional task ID assigned by the daemon loop
	AttemptID       string               // optional attempt ID for v2 tracking (propagated to child runs)
	WorkflowName    string               // workflow name for run context seeding
	ExtraEnv        []string             // additional KEY=VALUE env vars for all steps
	ResumeStep      string               // step being resumed (for prompt conversation resume)

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
			tempFileDir := filepath.Join(".cloche", "runs", e.HostRunID)
			if mkdirErr := os.MkdirAll(filepath.Join(e.ProjectDir, tempFileDir), 0755); mkdirErr != nil {
				log.Printf("host executor: creating temp_file_dir %q: %v", tempFileDir, mkdirErr)
			}
			pairs := [][2]string{
				{"task_id", e.TaskID},
				{"attempt_id", e.AttemptID},
				{"workflow", e.WorkflowName},
				{"run_id", e.HostRunID},
				{"temp_file_dir", tempFileDir},
			}
			for _, p := range pairs {
				if err := e.Store.SetContextKey(ctx, e.TaskID, e.AttemptID, e.HostRunID, p[0], p[1]); err != nil {
					log.Printf("host executor: seeding run context key %q: %v", p[0], err)
				}
			}
		})
	}

	// Update workflow key on every step (handles workflow transitions).
	if e.TaskID != "" && e.Store != nil {
		if err := e.Store.SetContextKey(ctx, e.TaskID, e.AttemptID, e.HostRunID, "workflow", e.WorkflowName); err != nil {
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
			if err := e.Store.SetContextKey(ctx, e.TaskID, e.AttemptID, e.HostRunID, kv[0], kv[1]); err != nil {
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
	case domain.StepTypeAgent:
		result, err = e.executeAgent(ctx, step)
	case domain.StepTypeHuman:
		r, e2 := e.executeHumanStep(ctx, step)
		result, err = domain.StepResult{Result: r}, e2
	default:
		return domain.StepResult{}, fmt.Errorf("unsupported step type %q in host workflow", step.Type)
	}

	// Record the step result after execution.
	if e.TaskID != "" && e.Store != nil && err == nil && result.Result != "" {
		key := fmt.Sprintf("%s:%s:result", e.WorkflowName, step.Name)
		if setErr := e.Store.SetContextKey(ctx, e.TaskID, e.AttemptID, e.HostRunID, key, result.Result); setErr != nil {
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
	// Build env from parent, filtering out CLOCHE_* vars so they don't
	// leak from the container environment when they are not explicitly set.
	var baseEnv []string
	for _, ev := range os.Environ() {
		if strings.HasPrefix(ev, "CLOCHE_RUN_ID=") ||
			strings.HasPrefix(ev, "CLOCHE_TASK_ID=") ||
			strings.HasPrefix(ev, "CLOCHE_ATTEMPT_ID=") {
			continue
		}
		baseEnv = append(baseEnv, ev)
	}
	cmd.Env = append(baseEnv,
		"CLOCHE_PROJECT_DIR="+e.ProjectDir,
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

	// Pass extra env vars
	if len(e.ExtraEnv) > 0 {
		cmd.Env = append(cmd.Env, e.ExtraEnv...)
	}

	// Pass previous step output if available
	if prevOutput := e.findPrevOutput(step); prevOutput != "" {
		cmd.Env = append(cmd.Env, "CLOCHE_PREV_OUTPUT="+prevOutput)
	}

	output, err := cmd.CombinedOutput()

	// Extract result marker
	markerResult, cleanOutput, found := protocol.ExtractResult(output)

	// Write output to file
	if mkErr := os.MkdirAll(e.OutputDir, 0755); mkErr == nil {
		_ = os.WriteFile(e.stepOutputPath(step.Name), cleanOutput, 0644)
	}

	if err != nil {
		if ctx.Err() != nil {
			return "timeout", nil
		}
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
		if ctx.Err() != nil {
			return domain.StepResult{Result: "timeout"}, nil
		}
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

// executeHumanStep handles a human step. When a PollCoordinator is configured
// (production/loop-driven mode), the executor registers a session and blocks
// on a result channel — the loop drives all poll timing via DrivePolls.
//
// When no coordinator is set (standalone/test mode), a self-contained polling
// loop is used for backward compatibility.
//
// The step's timeout (default 72h) is enforced by the context passed in from
// the engine.
func (e *Executor) executeHumanStep(ctx context.Context, step *domain.Step) (string, error) {
	intervalStr := step.Config["interval"]
	interval, err := time.ParseDuration(intervalStr)
	if err != nil {
		return "", fmt.Errorf("human step %q: invalid interval %q: %w", step.Name, intervalStr, err)
	}

	// Mark the run as waiting so cloche list/status surfaces it distinctly.
	if e.Store != nil && e.HostRunID != "" {
		if run, getErr := e.Store.GetRun(ctx, e.HostRunID); getErr == nil {
			run.State = domain.RunStateWaiting
			if updateErr := e.Store.UpdateRun(ctx, run); updateErr != nil {
				log.Printf("host executor: setting run %q to waiting: %v", e.HostRunID, updateErr)
			}
		}
	}

	// Record poll state in HumanPollStore for observability.
	if e.HumanPollStore != nil && e.HostRunID != "" {
		_ = e.HumanPollStore.UpsertHumanPoll(ctx, &ports.HumanPollRecord{
			RunID:      e.HostRunID,
			StepName:   step.Name,
			StartedAt:  time.Now(),
			LastPollAt: time.Now(),
		})
	}

	invokeFn := func(invCtx context.Context) (string, error) {
		return e.runHumanPollScript(invCtx, step)
	}

	if e.PollCoord != nil {
		// Loop-driven mode: register session and block until the loop delivers
		// a decision via DrivePolls. The executor goroutine is parked here; the
		// orchestration loop owns all poll timing.
		resultCh := e.PollCoord.Register(e.HostRunID, step.Name, invokeFn, interval)
		defer e.PollCoord.Unregister(e.HostRunID, step.Name)
		defer func() {
			if e.HumanPollStore != nil && e.HostRunID != "" {
				_ = e.HumanPollStore.DeleteHumanPoll(context.Background(), e.HostRunID, step.Name)
			}
		}()
		select {
		case result := <-resultCh:
			return result, nil
		case <-ctx.Done():
			return "timeout", nil
		}
	}

	// Standalone mode (no coordinator): self-contained polling loop.
	// Used when no orchestration loop is configured (e.g. executor unit tests).
	return e.executeHumanStepStandalone(ctx, step, interval, invokeFn)
}

// executeHumanStepStandalone is the self-contained polling loop used when no
// PollCoordinator is configured. It invokes the script at the configured
// interval, handling overlapping invocations and 4× overage failure.
func (e *Executor) executeHumanStepStandalone(
	ctx context.Context,
	step *domain.Step,
	interval time.Duration,
	invokeFn func(context.Context) (string, error),
) (string, error) {
	// Maximum time allowed for a single invocation before the step is failed.
	maxInvocationTime := 4 * interval

	// checkInterval is how often the loop wakes to decide whether to poll.
	const humanPollCheckInterval = 30 * time.Second
	checkInterval := humanPollCheckInterval
	if interval < checkInterval {
		checkInterval = interval / 2
		if checkInterval < time.Second {
			checkInterval = time.Second
		}
	}

	type pollResult struct {
		result string
		err    error
	}

	// pollCh carries the result of the current invocation goroutine.
	// Buffered so the goroutine never blocks if we exit early.
	pollCh := make(chan pollResult, 1)

	invocationRunning := false
	var invocationStart time.Time

	// Set last poll to interval ago so the first poll fires immediately.
	lastPoll := time.Now().Add(-interval)

	for {
		now := time.Now()

		if invocationRunning {
			// Check whether the current invocation has finished.
			select {
			case pr := <-pollCh:
				invocationRunning = false
				lastPoll = now
				if pr.err != nil {
					return "", pr.err
				}
				if pr.result != "" {
					return pr.result, nil
				}
				// Empty result = pending; keep polling.
			default:
				// Still running — check for 4× overage.
				elapsed := now.Sub(invocationStart)
				if elapsed > maxInvocationTime {
					log.Printf("human step %q: invocation has been running for %v (>4× interval %v), failing step",
						step.Name, elapsed.Round(time.Second), interval)
					return "fail", nil
				}
			}
		} else if now.Sub(lastPoll) >= interval {
			// Time to start a new poll invocation.
			invocationRunning = true
			invocationStart = now
			log.Printf("human step %q: polling (last=%s interval=%s)", step.Name, lastPoll.Format(time.RFC3339), interval)
			go func() {
				result, pollErr := invokeFn(ctx)
				pollCh <- pollResult{result: result, err: pollErr}
			}()
		}

		select {
		case <-ctx.Done():
			return "timeout", nil
		case <-time.After(checkInterval):
		}
	}
}

// runHumanPollScript runs a single invocation of the human step's polling script.
// It returns the wire name if a result marker was found, an empty string when the
// script exited 0 with no marker (pending), or "fail" on non-zero exit with no marker.
func (e *Executor) runHumanPollScript(ctx context.Context, step *domain.Step) (string, error) {
	scriptCmd := step.Config["poll"]
	cmd := exec.CommandContext(ctx, "sh", "-c", scriptCmd)
	cmd.Dir = e.scriptDir()

	var baseEnv []string
	for _, ev := range os.Environ() {
		if strings.HasPrefix(ev, "CLOCHE_RUN_ID=") ||
			strings.HasPrefix(ev, "CLOCHE_TASK_ID=") ||
			strings.HasPrefix(ev, "CLOCHE_ATTEMPT_ID=") {
			continue
		}
		baseEnv = append(baseEnv, ev)
	}
	cmd.Env = append(baseEnv,
		"CLOCHE_PROJECT_DIR="+e.ProjectDir,
	)
	if e.HostRunID != "" {
		cmd.Env = append(cmd.Env, "CLOCHE_RUN_ID="+e.HostRunID)
	}
	if e.TaskID != "" {
		cmd.Env = append(cmd.Env, "CLOCHE_TASK_ID="+e.TaskID)
	}
	if e.AttemptID != "" {
		cmd.Env = append(cmd.Env, "CLOCHE_ATTEMPT_ID="+e.AttemptID)
	}
	if len(e.ExtraEnv) > 0 {
		cmd.Env = append(cmd.Env, e.ExtraEnv...)
	}

	output, err := cmd.CombinedOutput()

	markerResult, cleanOutput, found := protocol.ExtractResult(output)

	if mkErr := os.MkdirAll(e.OutputDir, 0755); mkErr == nil {
		_ = os.WriteFile(e.stepOutputPath(step.Name), cleanOutput, 0644)
	}

	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			if found {
				return markerResult, nil
			}
			return "fail", nil
		}
		return "", err
	}

	if found {
		return markerResult, nil
	}
	// Exit 0, no marker: pending.
	return "", nil
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
