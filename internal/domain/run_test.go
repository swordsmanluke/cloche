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

func TestWorseState(t *testing.T) {
	tests := []struct {
		name string
		a, b domain.RunState
		want domain.RunState
	}{
		{"succeeded vs succeeded", domain.RunStateSucceeded, domain.RunStateSucceeded, domain.RunStateSucceeded},
		{"succeeded vs failed", domain.RunStateSucceeded, domain.RunStateFailed, domain.RunStateFailed},
		{"failed vs succeeded", domain.RunStateFailed, domain.RunStateSucceeded, domain.RunStateFailed},
		{"succeeded vs cancelled", domain.RunStateSucceeded, domain.RunStateCancelled, domain.RunStateCancelled},
		{"cancelled vs failed", domain.RunStateCancelled, domain.RunStateFailed, domain.RunStateFailed},
		{"failed vs failed", domain.RunStateFailed, domain.RunStateFailed, domain.RunStateFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.WorseState(tt.a, tt.b)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestTaskAggregateStatus(t *testing.T) {
	now := time.Now()
	mkRun := func(state domain.RunState, startedAt time.Time) *domain.Run {
		r := domain.NewRun("r", "wf")
		r.State = state
		r.StartedAt = startedAt
		return r
	}
	mkHostRun := func(state domain.RunState, startedAt time.Time) *domain.Run {
		r := mkRun(state, startedAt)
		r.IsHost = true
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
		{
			name: "host run failed with succeeded child - host wins",
			runs: []*domain.Run{
				mkHostRun(domain.RunStateFailed, now.Add(-1*time.Minute)),
				mkRun(domain.RunStateSucceeded, now),
			},
			want: domain.RunStateFailed,
		},
		{
			name: "host run succeeded with succeeded child",
			runs: []*domain.Run{
				mkHostRun(domain.RunStateSucceeded, now.Add(-1*time.Minute)),
				mkRun(domain.RunStateSucceeded, now),
			},
			want: domain.RunStateSucceeded,
		},
		{
			name: "most recent host run wins over older host",
			runs: []*domain.Run{
				mkHostRun(domain.RunStateSucceeded, now.Add(-5*time.Minute)),
				mkRun(domain.RunStateSucceeded, now.Add(-4*time.Minute)),
				mkHostRun(domain.RunStateFailed, now.Add(-2*time.Minute)),
				mkRun(domain.RunStateSucceeded, now),
			},
			want: domain.RunStateFailed,
		},
		{
			name: "active outweighs failed host",
			runs: []*domain.Run{
				mkHostRun(domain.RunStateFailed, now.Add(-2*time.Minute)),
				mkRun(domain.RunStateRunning, now),
			},
			want: domain.RunStateRunning,
		},
		{
			name: "waiting is active",
			runs: []*domain.Run{
				mkRun(domain.RunStateWaiting, now),
			},
			want: domain.RunStateWaiting,
		},
		{
			name: "running outweighs waiting",
			runs: []*domain.Run{
				mkRun(domain.RunStateWaiting, now.Add(-1*time.Minute)),
				mkRun(domain.RunStateRunning, now),
			},
			want: domain.RunStateRunning,
		},
		{
			name: "waiting outweighs pending",
			runs: []*domain.Run{
				mkRun(domain.RunStatePending, now.Add(-1*time.Minute)),
				mkRun(domain.RunStateWaiting, now),
			},
			want: domain.RunStateWaiting,
		},
		{
			name: "waiting outweighs terminal",
			runs: []*domain.Run{
				mkRun(domain.RunStateFailed, now.Add(-2*time.Minute)),
				mkRun(domain.RunStateWaiting, now),
			},
			want: domain.RunStateWaiting,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.TaskAggregateStatus(tt.runs)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestAttemptAggregateStatus(t *testing.T) {
	now := time.Now()
	// Each call to mkRun produces a run with a unique workflow name so that
	// deduplication in AttemptAggregateStatus does not collapse distinct runs
	// that happen to be built with the same helper.
	n := 0
	mkRun := func(state domain.RunState) *domain.Run {
		n++
		r := domain.NewRun("r", "wf"+string(rune('a'+n-1)))
		r.State = state
		r.StartedAt = now
		return r
	}
	mkHostRun := func(state domain.RunState) *domain.Run {
		r := mkRun(state)
		r.IsHost = true
		return r
	}
	// mkRunNamed creates a run with an explicit workflow name and start time,
	// for testing deduplication behaviour.
	mkRunNamed := func(state domain.RunState, wfName string, startedAt time.Time) *domain.Run {
		r := domain.NewRun("r", wfName)
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
			runs: []*domain.Run{mkRun(domain.RunStateSucceeded)},
			want: domain.RunStateSucceeded,
		},
		{
			name: "single failed",
			runs: []*domain.Run{mkRun(domain.RunStateFailed)},
			want: domain.RunStateFailed,
		},
		{
			name: "any failed means attempt failed",
			runs: []*domain.Run{
				mkHostRun(domain.RunStateSucceeded),
				mkRun(domain.RunStateFailed),
			},
			want: domain.RunStateFailed,
		},
		{
			name: "child failed overrides host succeeded",
			runs: []*domain.Run{
				mkHostRun(domain.RunStateSucceeded),
				mkRun(domain.RunStateFailed),
				mkRun(domain.RunStateSucceeded),
			},
			want: domain.RunStateFailed,
		},
		{
			name: "failed beats cancelled",
			runs: []*domain.Run{
				mkRun(domain.RunStateCancelled),
				mkRun(domain.RunStateFailed),
			},
			want: domain.RunStateFailed,
		},
		{
			name: "cancelled beats succeeded",
			runs: []*domain.Run{
				mkRun(domain.RunStateSucceeded),
				mkRun(domain.RunStateCancelled),
			},
			want: domain.RunStateCancelled,
		},
		{
			name: "running outweighs failed",
			runs: []*domain.Run{
				mkRun(domain.RunStateFailed),
				mkRun(domain.RunStateRunning),
			},
			want: domain.RunStateRunning,
		},
		{
			name: "pending outweighs terminal",
			runs: []*domain.Run{
				mkRun(domain.RunStateFailed),
				mkRun(domain.RunStatePending),
			},
			want: domain.RunStatePending,
		},
		{
			name: "running beats pending",
			runs: []*domain.Run{
				mkRun(domain.RunStatePending),
				mkRun(domain.RunStateRunning),
			},
			want: domain.RunStateRunning,
		},
		{
			name: "all succeeded",
			runs: []*domain.Run{
				mkRun(domain.RunStateSucceeded),
				mkRun(domain.RunStateSucceeded),
			},
			want: domain.RunStateSucceeded,
		},
		{
			// Regression: when a workflow is re-run after a failure, the new
			// succeeded run supersedes the old failed run. The task should
			// not remain 'failed' after a successful re-run of the same workflow.
			name: "re-run workflow supersedes earlier failure",
			runs: []*domain.Run{
				mkRunNamed(domain.RunStateSucceeded, "main", now),
				mkRunNamed(domain.RunStateFailed, "post-merge", now.Add(time.Second)),
				mkRunNamed(domain.RunStateSucceeded, "post-merge", now.Add(2*time.Second)),
			},
			want: domain.RunStateSucceeded,
		},
		{
			// A re-run that itself fails should still produce failed.
			name: "re-run workflow that also fails stays failed",
			runs: []*domain.Run{
				mkRunNamed(domain.RunStateSucceeded, "main", now),
				mkRunNamed(domain.RunStateFailed, "post-merge", now.Add(time.Second)),
				mkRunNamed(domain.RunStateFailed, "post-merge", now.Add(2*time.Second)),
			},
			want: domain.RunStateFailed,
		},
		{
			// A re-run that is still running should report running.
			name: "re-run workflow still running reports running",
			runs: []*domain.Run{
				mkRunNamed(domain.RunStateSucceeded, "main", now),
				mkRunNamed(domain.RunStateFailed, "post-merge", now.Add(time.Second)),
				mkRunNamed(domain.RunStateRunning, "post-merge", now.Add(2*time.Second)),
			},
			want: domain.RunStateRunning,
		},
		{
			name: "waiting is active",
			runs: []*domain.Run{mkRun(domain.RunStateWaiting)},
			want: domain.RunStateWaiting,
		},
		{
			name: "running outweighs waiting",
			runs: []*domain.Run{
				mkRun(domain.RunStateWaiting),
				mkRun(domain.RunStateRunning),
			},
			want: domain.RunStateRunning,
		},
		{
			name: "waiting outweighs pending",
			runs: []*domain.Run{
				mkRun(domain.RunStatePending),
				mkRun(domain.RunStateWaiting),
			},
			want: domain.RunStateWaiting,
		},
		{
			name: "waiting outweighs terminal",
			runs: []*domain.Run{
				mkRun(domain.RunStateFailed),
				mkRun(domain.RunStateWaiting),
			},
			want: domain.RunStateWaiting,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := domain.AttemptAggregateStatus(tt.runs)
			assert.Equal(t, tt.want, got)
		})
	}
}
