package engine_test

import (
	"context"
	"sync"
	"testing"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// buildTokenLimitWorkflow constructs a single-step workflow for token-limit tests.
// stepConfig controls step.Config["token-limit"]; wfTokenLimit controls wf.Config["token-limit"].
// Pass empty string to omit a config value.
func buildTokenLimitWorkflow(stepTokenLimit, wfTokenLimit string) *domain.Workflow {
	step := &domain.Step{
		Name:    "work",
		Type:    domain.StepTypeScript,
		Results: []string{"success", "timeout", "token-limit"},
		Config:  map[string]string{"run": "echo work"},
	}
	if stepTokenLimit != "" {
		step.Config["token-limit"] = stepTokenLimit
	}

	wf := &domain.Workflow{
		Name:      "test",
		EntryStep: "work",
		Steps:     map[string]*domain.Step{"work": step},
		Wiring: []domain.Wire{
			{From: "work", Result: "success", To: domain.StepDone},
			{From: "work", Result: "timeout", To: domain.StepAbort},
			{From: "work", Result: "token-limit", To: domain.StepAbort, Implicit: true},
		},
		Config: map[string]string{},
	}
	if wfTokenLimit != "" {
		wf.Config["token-limit"] = wfTokenLimit
	}
	return wf
}

// buildTwoStepWorkflow builds a sequential two-step workflow (step1 → step2 → done).
// Both steps have token-limit disabled at the step level; wfTokenLimit sets the workflow limit.
func buildTwoStepWorkflow(wfTokenLimit string) *domain.Workflow {
	makeStep := func(name string) *domain.Step {
		return &domain.Step{
			Name:    name,
			Type:    domain.StepTypeScript,
			Results: []string{"success", "token-limit"},
			Config:  map[string]string{"run": "echo " + name, "token-limit": "-1"},
		}
	}
	wf := &domain.Workflow{
		Name:      "test",
		EntryStep: "step1",
		Steps:     map[string]*domain.Step{"step1": makeStep("step1"), "step2": makeStep("step2")},
		Wiring: []domain.Wire{
			{From: "step1", Result: "success", To: "step2"},
			{From: "step2", Result: "success", To: domain.StepDone},
			{From: "step1", Result: "token-limit", To: domain.StepAbort, Implicit: true},
			{From: "step2", Result: "token-limit", To: domain.StepAbort, Implicit: true},
		},
		Config: map[string]string{},
	}
	if wfTokenLimit != "" {
		wf.Config["token-limit"] = wfTokenLimit
	}
	return wf
}

// usageExec is a step executor that returns fixed token usage per call.
type usageExec struct {
	mu          sync.Mutex
	callCount   int
	outputTokens int64
	inputTokens  int64
}

func (u *usageExec) Execute(_ context.Context, _ *domain.Step) (domain.StepResult, error) {
	u.mu.Lock()
	u.callCount++
	u.mu.Unlock()
	return domain.StepResult{
		Result: "success",
		Usage:  &domain.TokenUsage{OutputTokens: u.outputTokens, InputTokens: u.inputTokens},
	}, nil
}

// ─── Step-level enforcement ───────────────────────────────────────────────────

func TestEngine_StepTokenLimit_ExceedsLimit(t *testing.T) {
	wf := buildTokenLimitWorkflow("1000", "")
	exec := &usageExec{outputTokens: 1500}
	eng := engine.New(exec)

	run, _ := eng.Run(context.Background(), wf)

	assert.Equal(t, domain.RunStateFailed, run.State)
	require.Len(t, run.StepExecutions, 1)
	assert.Equal(t, "token-limit", run.StepExecutions[0].Result)
}

func TestEngine_StepTokenLimit_WithinLimit(t *testing.T) {
	wf := buildTokenLimitWorkflow("1000", "")
	exec := &usageExec{outputTokens: 500}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)

	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	require.Len(t, run.StepExecutions, 1)
	assert.Equal(t, "success", run.StepExecutions[0].Result)
}

// Acceptance criterion 3: default step limit is DefaultStepTokenLimit.
func TestEngine_StepTokenLimit_DefaultApplied(t *testing.T) {
	wf := buildTokenLimitWorkflow("", "") // no step token-limit set
	exec := &usageExec{outputTokens: engine.DefaultStepTokenLimit + 1}
	eng := engine.New(exec)

	run, _ := eng.Run(context.Background(), wf)

	assert.Equal(t, domain.RunStateFailed, run.State)
	require.Len(t, run.StepExecutions, 1)
	assert.Equal(t, "token-limit", run.StepExecutions[0].Result)
}

// Acceptance criterion 5: input tokens alone do not trigger step-level abort.
func TestEngine_StepTokenLimit_InputTokensIgnored(t *testing.T) {
	wf := buildTokenLimitWorkflow("1000", "")
	exec := &usageExec{outputTokens: 500, inputTokens: 50_000}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)

	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Equal(t, "success", run.StepExecutions[0].Result)
}

// Acceptance criterion 6: token-limit -1 disables step-level enforcement.
// Uses 1_000_000 tokens: above the default step limit (500k) but below the
// default workflow limit (2M), so only the step-level -1 sentinel is tested.
func TestEngine_StepTokenLimit_Disabled(t *testing.T) {
	wf := buildTokenLimitWorkflow("-1", "")
	exec := &usageExec{outputTokens: 1_000_000}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)

	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.Equal(t, "success", run.StepExecutions[0].Result)
}

// Acceptance criterion 7: token-limit 0 short-circuits before calling executor.
func TestEngine_StepTokenLimit_ZeroNoCall(t *testing.T) {
	wf := buildTokenLimitWorkflow("0", "")
	exec := &usageExec{}
	eng := engine.New(exec)

	run, _ := eng.Run(context.Background(), wf)

	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Equal(t, 0, exec.callCount, "executor must not be called when token-limit = 0")
	require.Len(t, run.StepExecutions, 1)
	assert.Equal(t, "token-limit", run.StepExecutions[0].Result)
}

// ─── Workflow-level enforcement ───────────────────────────────────────────────

// Acceptance criterion 4: cumulative output tokens crossing workflow limit aborts.
func TestEngine_WorkflowTokenLimit_Cumulative(t *testing.T) {
	wf := buildTwoStepWorkflow("3000")
	exec := &usageExec{outputTokens: 2000}
	eng := engine.New(exec)

	run, _ := eng.Run(context.Background(), wf)

	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Equal(t, 2, exec.callCount) // both steps executed
}

// Acceptance criterion 3: default workflow limit is DefaultWorkflowTokenLimit.
func TestEngine_WorkflowTokenLimit_DefaultApplied(t *testing.T) {
	wf := buildTwoStepWorkflow("") // no workflow token-limit — default applies
	// Each step returns just over half the default, so two steps exceed it.
	perStep := engine.DefaultWorkflowTokenLimit/2 + 1
	exec := &usageExec{outputTokens: perStep}
	eng := engine.New(exec)

	run, _ := eng.Run(context.Background(), wf)

	assert.Equal(t, domain.RunStateFailed, run.State)
}

// Acceptance criterion 6: workflow token-limit -1 disables workflow-level enforcement.
func TestEngine_WorkflowTokenLimit_Disabled(t *testing.T) {
	wf := buildTwoStepWorkflow("-1")
	exec := &usageExec{outputTokens: 999_999_999}
	eng := engine.New(exec)

	run, err := eng.Run(context.Background(), wf)

	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
}

// Acceptance criterion 7: workflow token-limit 0 aborts before any step runs.
func TestEngine_WorkflowTokenLimit_ZeroNoCall(t *testing.T) {
	wf := buildTokenLimitWorkflow("", "0")
	exec := &usageExec{}
	eng := engine.New(exec)

	run, _ := eng.Run(context.Background(), wf)

	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.Equal(t, 0, exec.callCount, "no executor must be called when workflow token-limit = 0")
	assert.Empty(t, run.StepExecutions)
}
