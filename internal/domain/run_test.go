package domain_test

import (
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestRun_Lifecycle(t *testing.T) {
	run := domain.NewRun("run-1", "test-workflow")
	assert.Equal(t, domain.RunStatePending, run.State)

	run.Start()
	assert.Equal(t, domain.RunStateRunning, run.State)
	assert.False(t, run.StartedAt.IsZero())

	run.RecordStepStart("code")
	assert.Equal(t, []string{"code"}, run.ActiveSteps)
	assert.Len(t, run.StepExecutions, 1)

	run.RecordStepComplete("code", "success")
	assert.Empty(t, run.ActiveSteps)
	assert.Equal(t, "success", run.StepExecutions[0].Result)
	assert.False(t, run.StepExecutions[0].CompletedAt.IsZero())

	run.Complete(domain.RunStateSucceeded)
	assert.Equal(t, domain.RunStateSucceeded, run.State)
	assert.False(t, run.CompletedAt.IsZero())
}

func TestRunProjectDir(t *testing.T) {
	r := domain.NewRun("test-1", "develop")
	r.ProjectDir = "/home/user/project"
	assert.Equal(t, "/home/user/project", r.ProjectDir)
}

func TestStepExecutionCapturedData(t *testing.T) {
	exec := &domain.StepExecution{
		StepName:      "implement",
		PromptText:    "Write a hello world",
		AgentOutput:   "Here is the code...",
		AttemptNumber: 2,
	}
	assert.Equal(t, "Write a hello world", exec.PromptText)
	assert.Equal(t, "Here is the code...", exec.AgentOutput)
	assert.Equal(t, 2, exec.AttemptNumber)
}

func TestRun_Fail(t *testing.T) {
	run := domain.NewRun("run-1", "test-workflow")
	run.Start()
	run.Fail("container exploded")
	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.False(t, run.CompletedAt.IsZero())
	assert.Equal(t, "container exploded", run.ErrorMessage)
}

func TestRun_StepExecution_Duration(t *testing.T) {
	exec := &domain.StepExecution{
		StepName:    "code",
		StartedAt:   time.Now().Add(-5 * time.Second),
		CompletedAt: time.Now(),
	}
	assert.InDelta(t, 5.0, exec.Duration().Seconds(), 0.1)
}
