package agent

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/cloche-dev/cloche/internal/adapters/agents/generic"
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

	executor := &stepExecutor{
		workDir: r.cfg.WorkDir,
		generic: genericAdapter,
	}

	eng := engine.New(executor)
	eng.SetStatusHandler(&statusReporter{writer: statusWriter})

	run, err := eng.Run(ctx, wf)
	if err != nil {
		statusWriter.Error("", err.Error())
		statusWriter.RunCompleted("failed")
		return err
	}

	statusWriter.RunCompleted(string(run.State))
	return nil
}

type stepExecutor struct {
	workDir string
	generic *generic.Adapter
}

func (e *stepExecutor) Execute(ctx context.Context, step *domain.Step) (string, error) {
	switch step.Type {
	case domain.StepTypeScript:
		return e.generic.Execute(ctx, step, e.workDir)
	case domain.StepTypeAgent:
		if _, ok := step.Config["run"]; ok {
			return e.generic.Execute(ctx, step, e.workDir)
		}
		return "", fmt.Errorf("agent step %q requires an agent adapter (not yet implemented for prompt-based steps)", step.Name)
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
