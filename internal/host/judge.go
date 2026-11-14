package host

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/signal"
)

// RunJudge runs the project's judge host workflow (the project's own, else the
// built-in) for questionIDs, as a recorded run: it shows in run history and
// its agent step reports token usage like any other agent step.
//
// The built-in's prompt is the generic rules followed by guide (the bound qa
// module's judge.md). A project workflow brings its own prompt and reads
// question_ids and judge_guide with `cloche get`.
func RunJudge(ctx context.Context, runner *Runner, projectDir, guide string, questionIDs []string) error {
	store := runner.Store
	if store == nil {
		return fmt.Errorf("%s: no run store", signal.JudgeWorkflowName)
	}
	wf, err := findHostWorkflow(projectDir, signal.JudgeWorkflowName)
	if err != nil {
		return err
	}
	if wf.Builtin {
		if step := wf.Steps[signal.JudgeStep]; step != nil {
			step.Config["prompt"] = signal.JudgePrompt(guide, questionIDs)
		}
	}

	kvTask, kvAttempt := signal.JudgeWorkflowName, domain.GenerateAttemptID()
	defer func() { _ = store.DeleteContextKeys(context.Background(), kvTask, kvAttempt) }()
	for k, v := range map[string]string{
		"question_ids": strings.Join(questionIDs, "\n"),
		"judge_guide":  guide,
	} {
		if v == "" {
			continue
		}
		if err := store.SetContextKey(ctx, kvTask, kvAttempt, "", k, v); err != nil {
			return fmt.Errorf("%s: seeding %s: %w", signal.JudgeWorkflowName, k, err)
		}
	}

	r := *runner
	r.KVTaskID, r.KVAttemptID = kvTask, kvAttempt
	result, err := r.RunWorkflowDef(ctx, projectDir, wf, domain.GenerateRunID(signal.JudgeWorkflowName, kvAttempt))
	if result != nil && result.tmpOutputDir != "" {
		defer os.RemoveAll(result.tmpOutputDir)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", signal.JudgeWorkflowName, err)
	}
	if result.State != domain.RunStateSucceeded {
		return fmt.Errorf("%s workflow finished with state %s", signal.JudgeWorkflowName, result.State)
	}
	return nil
}

// JudgeLauncher starts the judge for freshly asked questions. It satisfies
// web.JudgeLauncher. A judge that cannot start, is gated off, or fails changes
// nothing: the questions stay unanswered for a human and the ask is unaffected.
type JudgeLauncher struct {
	// NewRunner builds the runner for a project's judge run.
	NewRunner func(projectDir string) *Runner
	// Blocked reports whether the budget launch gate is closed; nil means open.
	Blocked func() bool
	// BaseContext outlives the ask request, which ends when the response is
	// written. Defaults to context.Background().
	BaseContext context.Context

	wg sync.WaitGroup
}

// LaunchJudge starts the judge in the background and returns at once. It
// starts no run when the budget gate is closed or the bound qa module has no
// judge.md (nothing to follow).
func (l *JudgeLauncher) LaunchJudge(_ context.Context, projectDir string, questionIDs []string) error {
	if len(questionIDs) == 0 {
		return nil
	}
	if l.Blocked != nil && l.Blocked() {
		log.Printf("judge: budget launch gate is closed; leaving %d question(s) for %s unanswered", len(questionIDs), projectDir)
		return nil
	}
	guide, err := QAGuide(projectDir, true)
	if err != nil {
		return fmt.Errorf("judge not started: %w", err)
	}
	base := l.BaseContext
	if base == nil {
		base = context.Background()
	}
	runner := l.NewRunner(projectDir)
	ids := append([]string(nil), questionIDs...)
	l.wg.Add(1)
	go func() {
		defer l.wg.Done()
		if err := RunJudge(base, runner, projectDir, guide, ids); err != nil {
			log.Printf("judge: run for %s failed; questions %v stay unanswered: %v", projectDir, ids, err)
		}
	}()
	return nil
}

// Wait blocks until every started judge run has finished (tests, shutdown).
func (l *JudgeLauncher) Wait() { l.wg.Wait() }
