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

func TestRunTitle(t *testing.T) {
	r := domain.NewRun("test-1", "develop")
	r.Title = "Add dark mode toggle"
	assert.Equal(t, "Add dark mode toggle", r.Title)
}

func TestStepExecutionFields(t *testing.T) {
	exec := &domain.StepExecution{
		StepName: "implement",
		Result:   "success",
	}
	assert.Equal(t, "implement", exec.StepName)
	assert.Equal(t, "success", exec.Result)
}

func TestRun_Fail(t *testing.T) {
	run := domain.NewRun("run-1", "test-workflow")
	run.Start()
	run.Fail("container exploded")
	assert.Equal(t, domain.RunStateFailed, run.State)
	assert.False(t, run.CompletedAt.IsZero())
	assert.Equal(t, "container exploded", run.ErrorMessage)
}

func TestRunIsHost(t *testing.T) {
	r := domain.NewRun("host-1", "main")
	assert.False(t, r.IsHost)
	r.IsHost = true
	assert.True(t, r.IsHost)
}

func TestRun_StepExecution_Duration(t *testing.T) {
	exec := &domain.StepExecution{
		StepName:    "code",
		StartedAt:   time.Now().Add(-5 * time.Second),
		CompletedAt: time.Now(),
	}
	assert.InDelta(t, 5.0, exec.Duration().Seconds(), 0.1)
}

func TestTaskAggregateStatus(t *testing.T) {
	now := time.Now()
	mkRun := func(state domain.RunState, startedAt time.Time) *domain.Run {
		r := domain.NewRun("r", "wf")
		r.State = state
		r.StartedAt = startedAt
		return r
	}

	tests := []struct {
		name string
		runs []*domain.Run
		want domain.RunState
	}{
		{
			name: "empty returns pending",
			runs: nil,
			want: domain.RunStatePending,
		},
		{
			name: "single succeeded",
			runs: []*domain.Run{mkRun(domain.RunStateSucceeded, now)},
			want: domain.RunStateSucceeded,
		},
		{
			name: "single failed",
			runs: []*domain.Run{mkRun(domain.RunStateFailed, now)},
			want: domain.RunStateFailed,
		},
		{
			name: "running outweighs succeeded",
			runs: []*domain.Run{
				mkRun(domain.RunStateSucceeded, now.Add(-1*time.Minute)),
				mkRun(domain.RunStateRunning, now),
			},
			want: domain.RunStateRunning,
		},
		{
			name: "running outweighs failed",
			runs: []*domain.Run{
				mkRun(domain.RunStateFailed, now.Add(-1*time.Minute)),
				mkRun(domain.RunStateRunning, now),
			},
			want: domain.RunStateRunning,
		},
		{
			name: "pending outweighs terminal",
			runs: []*domain.Run{
				mkRun(domain.RunStateFailed, now.Add(-1*time.Minute)),
				mkRun(domain.RunStatePending, now),
			},
			want: domain.RunStatePending,
		},
		{
			name: "running outweighs pending",
			runs: []*domain.Run{
				mkRun(domain.RunStatePending, now.Add(-1*time.Minute)),
				mkRun(domain.RunStateRunning, now),
			},
			want: domain.RunStateRunning,
		},
		{
			name: "most recent attempt wins - success after failure",
			runs: []*domain.Run{
				mkRun(domain.RunStateFailed, now.Add(-2*time.Minute)),
				mkRun(domain.RunStateSucceeded, now),
			},
			want: domain.RunStateSucceeded,
		},
		{
			name: "most recent attempt wins - failure after success",
			runs: []*domain.Run{
				mkRun(domain.RunStateSucceeded, now.Add(-2*time.Minute)),
				mkRun(domain.RunStateFailed, now),
			},
			want: domain.RunStateFailed,
		},
		{
			name: "cancelled is terminal - most recent wins",
			runs: []*domain.Run{
				mkRun(domain.RunStateCancelled, now.Add(-2*time.Minute)),
				mkRun(domain.RunStateSucceeded, now),
			},
			want: domain.RunStateSucceeded,
		},
		{
			name: "active outweighs even most recent terminal",
			runs: []*domain.Run{
				mkRun(domain.RunStateRunning, now.Add(-5*time.Minute)),
				mkRun(domain.RunStateFailed, now),
			},
			want: domain.RunStateRunning,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.TaskAggregateStatus(tt.runs)
			assert.Equal(t, tt.want, got)
		})
	}
}
