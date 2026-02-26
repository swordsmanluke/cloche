package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

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
	RunID        string // Set by cloche-agent; empty disables result push
	GitRemote    string // git:// URL of the host's git daemon
}

type Runner struct {
	cfg      RunnerConfig
	mu       sync.Mutex
	captured map[string]prompt.CapturedData
}

func NewRunner(cfg RunnerConfig) *Runner {
	return &Runner{
		cfg:      cfg,
		captured: make(map[string]prompt.CapturedData),
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
	if cmd, ok := os.LookupEnv("CLOCHE_AGENT_COMMAND"); ok {
		promptAdapter.Command = cmd
	}

	executor := &stepExecutor{
		runner:  r,
		workDir: r.cfg.WorkDir,
		generic: genericAdapter,
		prompt:  promptAdapter,
	}

	// Reset per-run state from any previous run
	_ = os.RemoveAll(filepath.Join(r.cfg.WorkDir, ".cloche", "attempt_count"))
	_ = os.RemoveAll(filepath.Join(r.cfg.WorkDir, ".cloche", "output"))

	eng := engine.New(executor)
	eng.SetStatusHandler(&statusReporter{writer: statusWriter, runner: r})

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
	runID := r.cfg.RunID
	remote := r.cfg.GitRemote
	if runID == "" || remote == "" {
		return
	}
	branch := "cloche/" + runID
	dir := r.cfg.WorkDir

	gitEnv := append(os.Environ(),
		"GIT_AUTHOR_NAME=cloche", "GIT_AUTHOR_EMAIL=cloche@local",
		"GIT_COMMITTER_NAME=cloche", "GIT_COMMITTER_EMAIL=cloche@local",
	)

	// Phase 1: git setup — init, add, write-tree, fetch. Capture tree hash.
	// The agent (e.g. Claude Code) may reinitialize .git during execution,
	// losing the original history. We recover it by fetching from the host's
	// git daemon, then use git plumbing to create a commit whose tree is the
	// current working directory and whose parent is the original HEAD.
	setupScript := `set -e
git init >&2
mkdir -p .git/info
cat > .git/info/exclude << 'EXCLUDE'
# Cloche: exclude agent tooling noise from result branches
**/.claude/settings.local.json
.serena/
*.db-shm
*.db-wal
*.db-journal
EXCLUDE
git add -A
TREE=$(git write-tree)
git fetch "$1" >&2
echo "$TREE"
`
	cmd := exec.CommandContext(ctx, "sh", "-c", setupScript, "sh", remote)
	cmd.Dir = dir
	cmd.Env = gitEnv
	var setupOut, setupErr bytes.Buffer
	cmd.Stdout = &setupOut
	cmd.Stderr = &setupErr
	if err := cmd.Run(); err != nil {
		log.Printf("pushResults setup: %v: %s", err, setupErr.String())
		return
	}
	tree := strings.TrimSpace(setupOut.String())

	// Phase 2: diff stat for commit message context.
	diffCmd := exec.CommandContext(ctx, "git", "diff-tree", "--no-commit-id", "--stat", "FETCH_HEAD", tree)
	diffCmd.Dir = dir
	diffCmd.Env = gitEnv
	diffOut, _ := diffCmd.Output()
	diffStat := strings.TrimSpace(string(diffOut))

	// Phase 3: build commit message — LLM-generated with static fallback.
	fallbackMsg := fmt.Sprintf("cloche: %s run %s", workflowName, runID)
	if diffStat != "" {
		fallbackMsg += "\n\n" + diffStat
	}

	commitMsg := fallbackMsg
	if userPrompt := r.readUserPrompt(); userPrompt != "" && diffStat != "" {
		if msg, err := r.generateCommitMsg(ctx, userPrompt, diffStat); err == nil {
			commitMsg = msg
		}
	}

	// Phase 4: create commit and push.
	msgFile := filepath.Join(dir, ".git", "cloche-commit-msg")
	if err := os.WriteFile(msgFile, []byte(commitMsg), 0644); err != nil {
		log.Printf("pushResults write msg: %v", err)
		return
	}

	pushScript := `set -e
COMMIT=$(git commit-tree "$1" -p FETCH_HEAD -F "$2")
git push "$3" "$COMMIT":refs/heads/"$4"
`
	pushCmd := exec.CommandContext(ctx, "sh", "-c", pushScript, "sh", tree, msgFile, remote, branch)
	pushCmd.Dir = dir
	pushCmd.Env = gitEnv
	if out, err := pushCmd.CombinedOutput(); err != nil {
		log.Printf("pushResults push: %v: %s", err, out)
	}
}

// readUserPrompt reads the user prompt from .cloche/<runID>/prompt.txt.
func (r *Runner) readUserPrompt() string {
	if r.cfg.RunID == "" {
		return ""
	}
	path := filepath.Join(r.cfg.WorkDir, ".cloche", r.cfg.RunID, "prompt.txt")
	if data, err := os.ReadFile(path); err == nil {
		return strings.TrimSpace(string(data))
	}
	return ""
}

// generateCommitMsg invokes the LLM to produce a conventional commit message
// from the user's original prompt and the diff stat.
func (r *Runner) generateCommitMsg(ctx context.Context, userPrompt, diffStat string) (string, error) {
	llmPrompt := fmt.Sprintf(`Write a short git commit message for the following changes.

## Developer Request
%s

## Files Changed
%s

Rules:
- Subject line: type(scope): description (max 72 chars)
- Common types: feat, fix, refactor, test, docs, chore
- Add a blank line then 1-2 sentence body only if the change is non-obvious
- Do not wrap the message in markdown code fences
`, userPrompt, diffStat)

	command := "claude"
	if cmd, ok := os.LookupEnv("CLOCHE_AGENT_COMMAND"); ok {
		command = cmd
	}

	cmd := exec.CommandContext(ctx, command, "-p", "--output-format", "text", "--dangerously-skip-permissions")
	cmd.Dir = r.cfg.WorkDir
	cmd.Stdin = strings.NewReader(llmPrompt)

	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return "", err
	}

	msg := strings.TrimSpace(stdout.String())
	if msg == "" {
		return "", fmt.Errorf("empty response from LLM")
	}
	return msg, nil
}

type stepExecutor struct {
	runner  *Runner
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
			e.prompt.OnCapture = func(c prompt.CapturedData) {
				e.runner.mu.Lock()
				e.runner.captured[step.Name] = c
				e.runner.mu.Unlock()
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
	runner *Runner
}

func (s *statusReporter) OnStepStart(_ *domain.Run, step *domain.Step) {
	s.writer.StepStarted(step.Name)
}

func (s *statusReporter) OnStepComplete(_ *domain.Run, step *domain.Step, result string) {
	s.runner.mu.Lock()
	c, ok := s.runner.captured[step.Name]
	if ok {
		delete(s.runner.captured, step.Name)
	}
	s.runner.mu.Unlock()

	if ok {
		s.writer.StepCompletedWithCapture(step.Name, result, c.AgentOutput, c.AttemptNumber)
	} else {
		s.writer.StepCompleted(step.Name, result)
	}
}

func (s *statusReporter) OnRunComplete(_ *domain.Run) {}
