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

	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/config"
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

// Executor implements engine.StepExecutor for host workflow steps (scripts and
// local agents). Workflow_name step dispatch is handled by the DaemonExecutor.
type Executor struct {
	ProjectDir    string
	MainDir       string         // main branch worktree dir; scripts execute from here
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

	// Pass extra env vars
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
