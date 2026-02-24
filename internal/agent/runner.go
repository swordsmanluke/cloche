package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/cloche-dev/cloche/internal/adapters/agents/generic"
	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/protocol"
)

type RunnerConfig struct {
	WorkflowPath string
	WorkDir      string
	StatusOutput io.Writer
}

type Runner struct {
	cfg RunnerConfig
}

func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{cfg: cfg}
}

func (r *Runner) Run(ctx context.Context) error {
	data, err := os.ReadFile(r.cfg.WorkflowPath)
	if err != nil {
		return fmt.Errorf("reading workflow file: %w", err)
	}

	wf, err := dsl.Parse(string(data))
	if err != nil {
		return fmt.Errorf("parsing workflow: %w", err)
	}

	statusWriter := protocol.NewStatusWriter(r.cfg.StatusOutput)
	genericAdapter := generic.New()
	promptAdapter := prompt.New()
	if cmd, ok := os.LookupEnv("CLOCHE_AGENT_COMMAND"); ok {
		promptAdapter.Command = cmd
	}

	executor := &stepExecutor{
		workDir: r.cfg.WorkDir,
		generic: genericAdapter,
		prompt:  promptAdapter,
	}

	// Reset per-run state from any previous run
	_ = os.RemoveAll(filepath.Join(r.cfg.WorkDir, ".cloche", "attempt_count"))
	_ = os.RemoveAll(filepath.Join(r.cfg.WorkDir, ".cloche", "output"))

	eng := engine.New(executor)
	eng.SetStatusHandler(&statusReporter{writer: statusWriter})

	protocol.AppendHistoryMarker(r.cfg.WorkDir, "workflow:start "+wf.Name)

	run, err := eng.Run(ctx, wf)
	if err != nil {
		protocol.AppendHistoryMarker(r.cfg.WorkDir, "workflow:end "+wf.Name+" result:failed")
		statusWriter.Error("", err.Error())
		statusWriter.RunCompleted("failed")
		return err
	}

	protocol.AppendHistoryMarker(r.cfg.WorkDir, "workflow:end "+wf.Name+" result:"+string(run.State))

	r.pushResults(ctx, wf.Name)

	statusWriter.RunCompleted(string(run.State))
	return nil
}

func (r *Runner) pushResults(ctx context.Context, workflowName string) {
	runID := os.Getenv("CLOCHE_RUN_ID")
	remote := os.Getenv("CLOCHE_GIT_REMOTE")
	if runID == "" || remote == "" {
		return
	}
	branch := "cloche/" + runID
	dir := r.cfg.WorkDir

	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.name", "cloche"},
		{"git", "config", "user.email", "cloche@local"},
		{"git", "add", "-A"},
		{"git", "commit", "--allow-empty", "-m",
			fmt.Sprintf("cloche: %s run %s", workflowName, runID)},
		{"git", "push", remote, "HEAD:refs/heads/" + branch},
	}
	for _, args := range cmds {
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			log.Printf("pushResults: %v: %s", err, out)
		}
	}
}

type stepExecutor struct {
	workDir string
	generic *generic.Adapter
	prompt  *prompt.Adapter
}

func (e *stepExecutor) Execute(ctx context.Context, step *domain.Step) (string, error) {
	switch step.Type {
	case domain.StepTypeScript:
		return e.generic.Execute(ctx, step, e.workDir)
	case domain.StepTypeAgent:
		if _, ok := step.Config["run"]; ok {
			return e.generic.Execute(ctx, step, e.workDir)
		}
		if _, ok := step.Config["prompt"]; ok {
			if cmd := step.Config["agent_command"]; cmd != "" {
				e.prompt.Command = cmd
			}
			return e.prompt.Execute(ctx, step, e.workDir)
		}
		return "", fmt.Errorf("agent step %q requires either 'run' or 'prompt' config", step.Name)
	default:
		return "", fmt.Errorf("unknown step type: %s", step.Type)
	}
}

type statusReporter struct {
	writer *protocol.StatusWriter
}

func (s *statusReporter) OnStepStart(_ *domain.Run, step *domain.Step) {
	s.writer.StepStarted(step.Name)
}

func (s *statusReporter) OnStepComplete(_ *domain.Run, step *domain.Step, result string) {
	s.writer.StepCompleted(step.Name, result)
}

func (s *statusReporter) OnRunComplete(_ *domain.Run) {}
