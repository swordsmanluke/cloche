package attention

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/swordsmanluke/cloche/internal/domain"
)

func failedAttempt(id, task string, at time.Time, hostStep, childStep string) []*domain.Run {
	host := &domain.Run{ID: id, TaskID: task, IsHost: true, WorkflowName: "main", State: domain.RunStateFailed,
		StartedAt: at, ErrorMessage: "boom",
		StepExecutions: []*domain.StepExecution{{StepName: hostStep, Result: "fail"}}}
	runs := []*domain.Run{host}
	if childStep != "" {
		runs = append(runs, &domain.Run{ID: id + "-c", TaskID: task, ParentRunID: id, WorkflowName: "develop",
			State: domain.RunStateFailed, StartedAt: at,
			StepExecutions: []*domain.StepExecution{
				{StepName: "implement", Result: "success"}, {StepName: childStep, Result: "fail"}}})
	}
	return runs
}

func TestTaskFailureStreak(t *testing.T) {
	t0 := time.Now().Add(-time.Hour)
	var runs []*domain.Run
	runs = append(runs, failedAttempt("r1", "T-1", t0, "develop", "test")...)
	runs = append(runs, failedAttempt("r2", "T-1", t0.Add(time.Minute), "develop", "test")...)
	runs = append(runs, failedAttempt("r3", "T-1", t0.Add(2*time.Minute), "develop", "verify-changes")...)
	runs = append(runs, failedAttempt("o1", "T-2", t0, "develop", "")...)

	st := TaskFailureStreak(runs, "T-1")
	assert.Equal(t, 3, st.Count)
	assert.Equal(t, "r3", st.LatestRunID)
	assert.Equal(t, "verify-changes", st.FailedStep)
	assert.Equal(t, "boom", st.Error)

	// No child run: the host run's own failing step.
	st = TaskFailureStreak(runs, "T-2")
	assert.Equal(t, 1, st.Count)
	assert.Equal(t, "develop", st.FailedStep)

	// A success breaks the streak.
	ok := &domain.Run{ID: "r4", TaskID: "T-1", IsHost: true, WorkflowName: "main",
		State: domain.RunStateSucceeded, StartedAt: t0.Add(3 * time.Minute)}
	assert.Equal(t, 0, TaskFailureStreak(append(runs, ok), "T-1").Count)
}
