package grpc_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	server "github.com/swordsmanluke/cloche/internal/adapters/grpc"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/budget"
	"github.com/swordsmanluke/cloche/internal/domain"
)

type budgetReader struct{ pct float64 }

func (b *budgetReader) Read(context.Context) (budget.Reading, error) {
	return budget.Reading{WeeklyPercent: b.pct}, nil
}

// overCapMonitor returns a monitor already over a 5% cap.
func overCapMonitor(t *testing.T, grace time.Duration) (*budget.Monitor, *budgetReader) {
	t.Helper()
	r := &budgetReader{pct: 10}
	m := budget.NewMonitor(budget.Config{UsagePercentPerDay: 5, Grace: grace}, r, "")
	m.Check(context.Background()) // baseline
	r.pct = 20
	require.True(t, m.Check(context.Background()).Over)
	return m, r
}

func TestGetBudget_StatusWarningAndNeedsYou(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()

	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	// No budget configured: disabled, no attention item.
	resp, err := srv.GetBudget(ctx, &pb.GetBudgetRequest{})
	require.NoError(t, err)
	assert.False(t, resp.Enabled)

	m, r := overCapMonitor(t, time.Hour)
	srv.SetBudget(m)

	resp, err = srv.GetBudget(ctx, &pb.GetBudgetRequest{})
	require.NoError(t, err)
	assert.True(t, resp.Enabled)
	assert.True(t, resp.Over)
	assert.Equal(t, 5.0, resp.CapPercent)
	assert.Equal(t, 10.0, resp.UsedPercent)
	assert.NotEmpty(t, resp.ClearsAt)
	assert.NotEmpty(t, resp.GraceEndsAt)
	assert.Contains(t, resp.Description, "OVER CAP")

	att, err := srv.GetAttention(ctx, &pb.GetAttentionRequest{All: true})
	require.NoError(t, err)
	require.Len(t, att.Items, 1)
	assert.Equal(t, "budget", att.Items[0].Kind)
	assert.Contains(t, att.Items[0].Reason, "5% daily cap")
	assert.Contains(t, att.Items[0].Reason, "clears")

	// Usage back under the cap: the item goes away on its own.
	r.pct = 11
	m.Check(ctx)
	att, err = srv.GetAttention(ctx, &pb.GetAttentionRequest{All: true})
	require.NoError(t, err)
	assert.Empty(t, att.Items)
}

// A hand-started run is not blocked by the cap; it carries a warning instead.
func TestRunWorkflow_OverCapWarnsButResumeFlowStillWorks(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	m, _ := overCapMonitor(t, time.Hour)
	srv.SetBudget(m)

	// Reaching the budget warning on a successful start needs a real
	// workflow; the resume path below exercises the same wrapper.
	ctx := context.Background()
	hostRun := budgetParkedRun(t, ctx, store, srv)
	md := metadata.Pairs("x-cloche-resume-run-id", hostRun.ID)
	resp, err := srv.RunWorkflow(metadata.NewIncomingContext(ctx, md), &pb.RunWorkflowRequest{})
	require.NoError(t, err)
	assert.Contains(t, resp.BudgetWarning, "daily budget cap")
	assert.Contains(t, resp.BudgetWarning, "by hand")
}

func budgetParkedRun(t *testing.T, ctx context.Context, store *sqlite.Store, srv *server.ClocheServer) *domain.Run {
	t.Helper()
	taskID, attemptID := "user-bb22", "bb22"
	require.NoError(t, store.SaveTask(ctx, &domain.Task{ID: taskID, Title: "t", ProjectDir: "/proj", CreatedAt: time.Now()}))
	require.NoError(t, store.SaveAttempt(ctx, &domain.Attempt{ID: attemptID, TaskID: taskID, StartedAt: time.Now().Add(-time.Minute), Result: domain.AttemptResultRunning}))

	run := domain.NewRun(domain.GenerateRunID("main", attemptID), "main")
	run.TaskID, run.AttemptID, run.IsHost, run.ProjectDir = taskID, attemptID, true, "/proj"
	run.State = domain.RunStateRunning
	run.StartedAt = time.Now().Add(-time.Minute)
	run.ActiveSteps = []string{"build"}
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.UpdateRun(ctx, run))

	require.Equal(t, 1, srv.ParkRunsForBudget(ctx))
	got, err := store.GetRun(ctx, run.ID)
	require.NoError(t, err)
	return got
}

func TestParkRunsForBudget_ParksInFlightRunsAndRecordsStep(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	run := budgetParkedRun(t, ctx, store, srv)
	assert.Equal(t, domain.RunStateParked, run.State)

	// A finished run is left alone.
	done := domain.NewRun("done-run", "main")
	done.ProjectDir = "/proj"
	done.State = domain.RunStateSucceeded
	require.NoError(t, store.CreateRun(ctx, done))
	require.Equal(t, 0, srv.ParkRunsForBudget(ctx), "parked runs are no longer in flight")
	got, _ := store.GetRun(ctx, "done-run")
	assert.Equal(t, domain.RunStateSucceeded, got.State)

	step, ok, err := store.GetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, "_budget.park_step")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "build", step)
}

// The grace timer parks in-flight runs; cloche resume then continues them.
func TestBudgetGrace_ParksThenResumeContinues(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")
	srv.SetTaskStore(store)

	taskID, attemptID := "user-cc33", "cc33"
	require.NoError(t, store.SaveTask(ctx, &domain.Task{ID: taskID, Title: "t", ProjectDir: "/proj", CreatedAt: time.Now()}))
	require.NoError(t, store.SaveAttempt(ctx, &domain.Attempt{ID: attemptID, TaskID: taskID, StartedAt: time.Now().Add(-time.Minute), Result: domain.AttemptResultRunning}))
	run := domain.NewRun(domain.GenerateRunID("main", attemptID), "main")
	run.TaskID, run.AttemptID, run.IsHost, run.ProjectDir = taskID, attemptID, true, "/proj"
	run.State = domain.RunStateRunning
	run.StartedAt = time.Now().Add(-time.Minute)
	run.ActiveSteps = []string{"build"}
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.UpdateRun(ctx, run))

	// Hit the cap with a short grace: the timer must park the run.
	r := &budgetReader{pct: 1}
	m := budget.NewMonitor(budget.Config{UsagePercentPerDay: 5, Grace: 50 * time.Millisecond}, r, "")
	srv.SetBudget(m)
	m.Check(ctx)

	got, _ := store.GetRun(ctx, run.ID)
	require.Equal(t, domain.RunStateRunning, got.State, "under the cap nothing is touched")

	r.pct = 9
	require.True(t, m.Check(ctx).Over)
	got, _ = store.GetRun(ctx, run.ID)
	assert.Equal(t, domain.RunStateRunning, got.State, "in flight runs get the grace period first")

	require.Eventually(t, func() bool {
		g, _ := store.GetRun(ctx, run.ID)
		return g.State == domain.RunStateParked
	}, 3*time.Second, 10*time.Millisecond, "the grace timer parks the run")

	// cloche resume: the parked run continues as a new attempt from the step
	// that was interrupted, and the parked run stops counting as resumable.
	md := metadata.Pairs("x-cloche-resume-run-id", run.ID)
	resp, err := srv.RunWorkflow(metadata.NewIncomingContext(ctx, md), &pb.RunWorkflowRequest{})
	require.NoError(t, err)
	assert.NotEmpty(t, resp.RunId)
	assert.NotEqual(t, attemptID, resp.AttemptId)
	old, _ := store.GetRun(ctx, run.ID)
	assert.Equal(t, domain.RunStateCancelled, old.State)
}

func TestResume_PlainParkedRunStillRejected(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()
	ctx := context.Background()
	srv := server.NewClocheServerWithCaptures(store, store, nil, "")

	run := domain.NewRun("r-help", "main")
	run.ProjectDir = "/proj"
	run.State = domain.RunStateParked
	run.ParkedThreadID = "thread-1" // parked on a help thread, not the budget
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.UpdateRun(ctx, run))

	md := metadata.Pairs("x-cloche-resume-run-id", run.ID)
	_, err = srv.RunWorkflow(metadata.NewIncomingContext(ctx, md), &pb.RunWorkflowRequest{})
	assert.Error(t, err)
}
