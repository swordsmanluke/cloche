package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/adapters/agents/generic"
	"github.com/cloche-dev/cloche/internal/adapters/agents/prompt"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/protocol"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type RunnerConfig struct {
	WorkflowPath   string
	WorkDir        string
	StatusOutput   io.Writer
	RunID          string
	TaskID         string // task ID for runtime state paths (.cloche/runs/<task-id>/)
	AttemptID      string // attempt identifier
	ResumeFromStep string // when non-empty, resume the workflow from this step
	StartStep      string // when non-empty, start execution at this step instead of the entry step
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

	wf, err := dsl.ParseForContainer(string(data))
	if err != nil {
		return fmt.Errorf("parsing workflow: %w", err)
	}
	wf.ResolveAgents()

	statusWriter := protocol.NewStatusWriter(r.cfg.StatusOutput)
	genericAdapter := generic.New()
	genericAdapter.RunID = r.cfg.RunID
	promptAdapter := prompt.New()
	promptAdapter.RunID = r.cfg.RunID
	promptAdapter.TaskID = r.cfg.TaskID
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

	// Reset per-run state from any previous run — but preserve outputs when resuming
	if r.cfg.ResumeFromStep == "" {
		_ = os.RemoveAll(filepath.Join(r.cfg.WorkDir, ".cloche", "output"))
	}

	// Create unified log writer
	ulog, err := logstream.New(r.cfg.WorkDir)
	if err != nil {
		return fmt.Errorf("creating unified log: %w", err)
	}
	defer ulog.Close()

	genericAdapter.StatusWriter = statusWriter

	// Dial daemon for KV operations if CLOCHE_ADDR is set.
	var kvClient pb.ClocheServiceClient
	if addr := os.Getenv("CLOCHE_ADDR"); addr != "" {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			log.Printf("agent: failed to dial daemon for KV at %s: %v", addr, err)
		} else {
			kvClient = pb.NewClocheServiceClient(conn)
			defer conn.Close()
		}
	}

	executor := &stepExecutor{
		workDir:        r.cfg.WorkDir,
		workflowName:   wf.Name,
		taskID:         r.cfg.TaskID,
		attemptID:      r.cfg.AttemptID,
		kvClient:       kvClient,
		generic:        genericAdapter,
		prompt:         promptAdapter,
		logStream:      ulog,
		statusWriter:   statusWriter,
		resumeFromStep: r.cfg.ResumeFromStep,
	}

	eng := engine.New(executor)
	eng.SetStatusHandler(&statusReporter{writer: statusWriter, logStream: ulog})

	// Seed run-level auto-context keys before execution starts.
	if kvClient != nil && r.cfg.TaskID != "" {
		pairs := [][2]string{
			{"task_id", r.cfg.TaskID},
			{"attempt_id", r.cfg.AttemptID},
			{"workflow", wf.Name},
			{"run_id", r.cfg.RunID},
		}
		for _, p := range pairs {
			executor.setContextKey(ctx, p[0], p[1])
		}
	}

	// Resume mode: load completed step results and configure engine
	if r.cfg.ResumeFromStep != "" {
		preloaded, err := loadCompletedStepResults(r.cfg.WorkDir, wf, r.cfg.ResumeFromStep)
		if err != nil {
			return fmt.Errorf("loading completed step results for resume: %w", err)
		}
		eng.SetPreloadedResults(preloaded)
	}

	// Single-step mode: start execution at a specific step.
	if r.cfg.StartStep != "" {
		eng.SetStartStep(r.cfg.StartStep)
	}

	// Generate a run title from the prompt if one was not provided by the caller.
	// The daemon sets title from --title; if empty, the agent extracts from prompt.
	if title := extractTitle(r.cfg.WorkDir, r.cfg.TaskID); title != "" {
		statusWriter.RunTitle(title)
	}

	protocol.AppendHistoryMarker(r.cfg.WorkDir, "workflow:start "+wf.Name)

	run, runErr := eng.Run(ctx, wf)
	if runErr != nil {
		protocol.AppendHistoryMarker(r.cfg.WorkDir, "workflow:end "+wf.Name+" result:failed")
		ulog.Log(logstream.TypeStatus, "error: "+runErr.Error())
		ulog.Log(logstream.TypeStatus, "run_completed: failed")
		// Extract the failed step name from step executions so the daemon
		// can display which step caused the failure.
		failedStep := ""
		if run != nil {
			for _, se := range run.StepExecutions {
				if se.Result == "error" || se.Result == "fail" {
					failedStep = se.StepName
				}
			}
		}
		statusWriter.Error(failedStep, runErr.Error())
		statusWriter.RunCompleted("failed")
		return runErr
	}

	protocol.AppendHistoryMarker(r.cfg.WorkDir, "workflow:end "+wf.Name+" result:"+string(run.State))

	ulog.Log(logstream.TypeStatus, "run_completed: "+string(run.State))
	statusWriter.RunCompleted(string(run.State))
	return nil
}

type stepExecutor struct {
	workDir        string
	workflowName   string // used to prefix per-step log files (v2 layout)
	taskID         string
	attemptID      string
	kvClient       pb.ClocheServiceClient // optional: gRPC KV client; nil if CLOCHE_ADDR not set
	generic        *generic.Adapter
	prompt         *prompt.Adapter
	logStream      *logstream.Writer
	statusWriter   *protocol.StatusWriter
	resumeFromStep string // the step being resumed (for prompt resume mode)
}

// setContextKey calls the daemon's SetContextKey RPC if a KV client is configured.
func (e *stepExecutor) setContextKey(ctx context.Context, key, value string) {
	if e.kvClient == nil || e.taskID == "" {
		return
	}
	rCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := e.kvClient.SetContextKey(rCtx, &pb.SetContextKeyRequest{
		TaskId:    e.taskID,
		AttemptId: e.attemptID,
		Key:       key,
		Value:     value,
	})
	if err != nil {
		log.Printf("agent: SetContextKey %q: %v", key, err)
	}
}

func (e *stepExecutor) Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
	// Update workflow key and seed prev_step/prev_step_exit before executing the step.
	if trigger, ok := engine.GetStepTrigger(ctx); ok {
		e.setContextKey(ctx, "workflow", e.workflowName)
		e.setContextKey(ctx, "prev_step", trigger.PrevStep)
		e.setContextKey(ctx, "prev_step_exit", trigger.PrevResult)
	}

	var sr domain.StepResult
	var err error

	switch step.Type {
	case domain.StepTypeScript:
		sr, err = e.generic.Execute(ctx, step, e.workDir)
		e.logStepOutput(step.Name, logstream.TypeScript)
	case domain.StepTypeAgent:
		if _, ok := step.Config["run"]; ok {
			sr, err = e.generic.Execute(ctx, step, e.workDir)
			e.logStepOutput(step.Name, logstream.TypeScript)
		} else if _, ok := step.Config["prompt"]; ok {
			if cmd := step.Config["agent_command"]; cmd != "" {
				e.prompt.Commands = prompt.ParseCommands(cmd)
			}
			if args := step.Config["agent_args"]; args != "" {
				e.prompt.ExplicitArgs = strings.Fields(args)
			}
			// Resume mode: if this is the step being resumed, use conversation resume
			if e.resumeFromStep == step.Name {
				e.prompt.ResumeConversation = true
				defer func() { e.prompt.ResumeConversation = false }()
			}
			sr, err = e.prompt.Execute(ctx, step, e.workDir)
			e.copyToLLMLog(step.Name)
			e.logStepOutput(step.Name, logstream.TypeLLM)
		} else {
			return domain.StepResult{}, fmt.Errorf("agent step %q requires either 'run' or 'prompt' config", step.Name)
		}
	default:
		return domain.StepResult{}, fmt.Errorf("unknown step type: %s", step.Type)
	}

	// Record step result after completion.
	if err == nil && sr.Result != "" {
		key := fmt.Sprintf("%s:%s:result", e.workflowName, step.Name)
		e.setContextKey(ctx, key, sr.Result)
	}

	return sr, err
}

// stepLogPath returns the v2 path for a per-step log file.
// Files are named <workflow>-<step>.log to avoid collisions across workflows.
func (e *stepExecutor) stepLogPath(stepName string) string {
	return filepath.Join(e.workDir, ".cloche", "output", e.workflowName+"-"+stepName+".log")
}

// logStepOutput reads the per-step log file and writes its contents to the unified log.
// It renames <step>.log to <workflow>-<step>.log (v2 layout) if not already done.
func (e *stepExecutor) logStepOutput(stepName string, typ logstream.EntryType) {
	newPath := e.stepLogPath(stepName)

	// If the v2 file doesn't exist yet, rename from the adapter-written path.
	if _, err := os.Stat(newPath); err != nil {
		oldPath := filepath.Join(e.workDir, ".cloche", "output", stepName+".log")
		if renErr := os.Rename(oldPath, newPath); renErr != nil {
			newPath = oldPath // fall back if rename fails
		}
	}

	data, err := os.ReadFile(newPath)
	if err != nil || len(data) == 0 {
		return
	}
	e.logStream.Log(typ, string(data))
}

// copyToLLMLog renames <step>.log to <workflow>-<step>.log and copies it to
// <workflow>-llm-<step>.log for agent prompt steps (v2 layout).
func (e *stepExecutor) copyToLLMLog(stepName string) {
	outputDir := filepath.Join(e.workDir, ".cloche", "output")
	srcPath := e.stepLogPath(stepName)
	dstPath := filepath.Join(outputDir, e.workflowName+"-llm-"+stepName+".log")

	// Rename from adapter-written path if v2 file doesn't exist yet.
	if _, err := os.Stat(srcPath); err != nil {
		oldPath := filepath.Join(outputDir, stepName+".log")
		if renErr := os.Rename(oldPath, srcPath); renErr != nil {
			// Fall back to old naming if rename fails.
			srcPath = oldPath
			dstPath = filepath.Join(outputDir, "llm-"+stepName+".log")
		}
	}

	data, err := os.ReadFile(srcPath)
	if err != nil {
		return
	}
	_ = os.WriteFile(dstPath, data, 0644)
}

type statusReporter struct {
	writer    *protocol.StatusWriter
	logStream *logstream.Writer
}

func (s *statusReporter) OnStepStart(_ *domain.Run, step *domain.Step) {
	s.writer.StepStarted(step.Name)
	s.logStream.Log(logstream.TypeStatus, "step_started: "+step.Name)
}

func (s *statusReporter) OnStepComplete(_ *domain.Run, step *domain.Step, result string, usage *domain.TokenUsage) {
	s.writer.StepCompleted(step.Name, result, usage)
	s.logStream.Log(logstream.TypeStatus, "step_completed: "+step.Name+" -> "+result)
}

func (s *statusReporter) OnRunComplete(_ *domain.Run) {}

// loadCompletedStepResults builds a map of step_name -> result for all steps
// that completed successfully before the resume point. It walks the workflow
// graph from the entry step, following wires, collecting results from the
// history log. Steps at or after the resumeFrom step are excluded.
func loadCompletedStepResults(workDir string, wf *domain.Workflow, resumeFrom string) (map[string]string, error) {
	preloaded := make(map[string]string)

	// Parse step results from history.log
	historyPath := filepath.Join(workDir, ".cloche", "history.log")
	data, err := os.ReadFile(historyPath)
	if err != nil {
		return nil, fmt.Errorf("reading history log: %w", err)
	}

	// Extract step:result pairs from history entries
	stepResults := make(map[string]string)
	for _, line := range strings.Split(string(data), "\n") {
		// History format: [timestamp] step:<name> result:<result> ...
		if !strings.Contains(line, "step:") || !strings.Contains(line, "result:") {
			continue
		}
		var stepName, result string
		for _, field := range strings.Fields(line) {
			if strings.HasPrefix(field, "step:") {
				stepName = strings.TrimPrefix(field, "step:")
			}
			if strings.HasPrefix(field, "result:") {
				result = strings.TrimPrefix(field, "result:")
			}
		}
		if stepName != "" && result != "" {
			stepResults[stepName] = result
		}
	}

	// Walk the workflow graph from entry step, collecting completed steps
	// until we reach the resume step
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

		// Follow wires from this step's result
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
	return preloaded, nil
}

// extractTitle tries to derive a one-line summary from the run's prompt content.
// It reads the prompt.txt file from .cloche/runs/<task-id>/ (written by the daemon
// before container start), then returns the first non-empty line, truncated to 100 characters.
func extractTitle(workDir string, taskID string) string {
	// Try the task-specific prompt file
	promptPath := filepath.Join(workDir, ".cloche", "runs", taskID, "prompt.txt")
	if _, err := os.Stat(promptPath); err != nil {
		// Fall back to legacy path
		promptPath = filepath.Join(workDir, ".cloche", "prompt.txt")
	}
	data, err := os.ReadFile(promptPath)
	if err != nil || len(data) == 0 {
		return ""
	}

	text := strings.TrimSpace(string(data))
	if text == "" {
		return ""
	}

	// Take first non-empty line as the title
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		// Skip markdown headers, comment markers, and blank lines
		if line == "" || line == "---" || line == "```" {
			continue
		}
		// Strip leading markdown header markers
		line = strings.TrimLeft(line, "# ")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 100 {
			line = line[:97] + "..."
		}
		return line
	}
	return ""
}
