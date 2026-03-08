package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloche-dev/cloche/internal/adapters/agents/generic"
	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/protocol"
)

type RunnerConfig struct {
	WorkflowPath string
	WorkDir      string
	StatusOutput io.Writer
	RunID        string
}

type Runner struct {
	cfg RunnerConfig
}

func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{
		cfg: cfg,
	}
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
	promptAdapter.RunID = r.cfg.RunID
	promptAdapter.StatusWriter = statusWriter
	if cmd, ok := os.LookupEnv("CLOCHE_AGENT_COMMAND"); ok {
		promptAdapter.Commands = prompt.ParseCommands(cmd)
	}
	if cmd := wf.Config["container.agent_command"]; cmd != "" {
		promptAdapter.Commands = prompt.ParseCommands(cmd)
	}
	if args := wf.Config["container.agent_args"]; args != "" {
		promptAdapter.ExplicitArgs = strings.Fields(args)
	}

	// Reset per-run state from any previous run
	_ = os.RemoveAll(filepath.Join(r.cfg.WorkDir, ".cloche", "attempt_count"))
	_ = os.RemoveAll(filepath.Join(r.cfg.WorkDir, ".cloche", "output"))

	// Create unified log writer
	ulog, err := logstream.New(r.cfg.WorkDir)
	if err != nil {
		return fmt.Errorf("creating unified log: %w", err)
	}
	defer ulog.Close()

	executor := &stepExecutor{
		workDir:   r.cfg.WorkDir,
		generic:   genericAdapter,
		prompt:    promptAdapter,
		logStream: ulog,
	}

	eng := engine.New(executor)
	eng.SetStatusHandler(&statusReporter{writer: statusWriter, logStream: ulog})

	protocol.AppendHistoryMarker(r.cfg.WorkDir, "workflow:start "+wf.Name)

	run, runErr := eng.Run(ctx, wf)
	if runErr != nil {
		protocol.AppendHistoryMarker(r.cfg.WorkDir, "workflow:end "+wf.Name+" result:failed")
		ulog.Log(logstream.TypeStatus, "error: "+runErr.Error())
		ulog.Log(logstream.TypeStatus, "run_completed: failed")
		statusWriter.Error("", runErr.Error())
		statusWriter.RunCompleted("failed")
		return runErr
	}

	protocol.AppendHistoryMarker(r.cfg.WorkDir, "workflow:end "+wf.Name+" result:"+string(run.State))

	ulog.Log(logstream.TypeStatus, "run_completed: "+string(run.State))
	statusWriter.RunCompleted(string(run.State))
	return nil
}

type stepExecutor struct {
	workDir   string
	generic   *generic.Adapter
	prompt    *prompt.Adapter
	logStream *logstream.Writer
}

func (e *stepExecutor) Execute(ctx context.Context, step *domain.Step) (string, error) {
	switch step.Type {
	case domain.StepTypeScript:
		result, err := e.generic.Execute(ctx, step, e.workDir)
		e.logStepOutput(step.Name, logstream.TypeScript)
		return result, err
	case domain.StepTypeAgent:
		if _, ok := step.Config["run"]; ok {
			result, err := e.generic.Execute(ctx, step, e.workDir)
			e.logStepOutput(step.Name, logstream.TypeScript)
			return result, err
		}
		if _, ok := step.Config["prompt"]; ok {
			if cmd := step.Config["agent_command"]; cmd != "" {
				e.prompt.Commands = prompt.ParseCommands(cmd)
			}
			if args := step.Config["agent_args"]; args != "" {
				e.prompt.ExplicitArgs = strings.Fields(args)
			}
			result, err := e.prompt.Execute(ctx, step, e.workDir)
			e.copyToLLMLog(step.Name)
			e.logStepOutput(step.Name, logstream.TypeLLM)
			return result, err
		}
		return "", fmt.Errorf("agent step %q requires either 'run' or 'prompt' config", step.Name)
	default:
		return "", fmt.Errorf("unknown step type: %s", step.Type)
	}
}

// logStepOutput reads the per-step log file and writes its contents to the unified log.
func (e *stepExecutor) logStepOutput(stepName string, typ logstream.EntryType) {
	path := filepath.Join(e.workDir, ".cloche", "output", stepName+".log")
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	e.logStream.Log(typ, string(data))
}

// copyToLLMLog copies <step>.log to llm-<step>.log for agent prompt steps.
func (e *stepExecutor) copyToLLMLog(stepName string) {
	src := filepath.Join(e.workDir, ".cloche", "output", stepName+".log")
	dst := filepath.Join(e.workDir, ".cloche", "output", "llm-"+stepName+".log")
	data, err := os.ReadFile(src)
	if err != nil {
		return
	}
	_ = os.WriteFile(dst, data, 0644)
}

type statusReporter struct {
	writer    *protocol.StatusWriter
	logStream *logstream.Writer
}

func (s *statusReporter) OnStepStart(_ *domain.Run, step *domain.Step) {
	s.writer.StepStarted(step.Name)
	s.logStream.Log(logstream.TypeStatus, "step_started: "+step.Name)
}

func (s *statusReporter) OnStepComplete(_ *domain.Run, step *domain.Step, result string) {
	s.writer.StepCompleted(step.Name, result)
	s.logStream.Log(logstream.TypeStatus, "step_completed: "+step.Name+" -> "+result)
}

func (s *statusReporter) OnRunComplete(_ *domain.Run) {}
