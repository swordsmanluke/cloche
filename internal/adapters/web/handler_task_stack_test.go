package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/attention"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/ports"
)

const taskStackProjectDir = "/home/user/projects/stackapp"

func getTaskStack(t *testing.T, h *Handler, query string) (*http.Response, TaskStack) {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/projects/stackapp/tasks/stack"+query, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	resp := w.Result()
	var stack TaskStack
	if resp.StatusCode == http.StatusOK {
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&stack))
	}
	return resp, stack
}

func TestAPITaskStack_Running(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	task := &domain.Task{ID: "task-1", Title: "Fix the bug", Source: domain.TaskSourceExternal, ProjectDir: taskStackProjectDir, CreatedAt: time.Now()}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := &domain.Attempt{ID: "att1", TaskID: "task-1", StartedAt: time.Now().Add(-2 * time.Minute), Result: domain.AttemptResultRunning}
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = "task-1"
	run.AttemptID = "att1"
	run.State = domain.RunStateRunning
	run.StartedAt = attempt.StartedAt
	run.ActiveSteps = []string{"build"}
	require.NoError(t, store.CreateRun(ctx, run))

	resp, stack := getTaskStack(t, h, "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, stack.Running, 1)
	entry := stack.Running[0]
	assert.Equal(t, "task-1", entry.TaskID)
	assert.Equal(t, "Fix the bug", entry.Title)
	assert.Equal(t, "run-1", entry.RunID)
	assert.Equal(t, 1, entry.Attempt)
	assert.Equal(t, "build", entry.CurrentStep)
	assert.GreaterOrEqual(t, entry.ElapsedSeconds, int64(100))
	assert.Empty(t, stack.Done)
	assert.Empty(t, stack.Queued)
	assert.Empty(t, stack.NeedsYou)
}

// TestAPITaskStack_Running_ElapsedFollowsRetriedChildRun covers a host run
// that re-dispatched a child container run for a retry: the Running row's
// elapsed must reflect the child's own start (the current retry), not the
// host run's original StartedAt from 40 minutes ago, while total_elapsed
// still exposes the full time since the task's first attempt.
func TestAPITaskStack_Running_ElapsedFollowsRetriedChildRun(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()
	now := time.Now()

	task := &domain.Task{ID: "task-retry", Title: "Flaky develop step", Source: domain.TaskSourceExternal, ProjectDir: taskStackProjectDir, CreatedAt: now}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := &domain.Attempt{ID: "att-retry", TaskID: "task-retry", StartedAt: now.Add(-40 * time.Minute), Result: domain.AttemptResultRunning}
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	host := domain.NewRun("host-run-1", "main")
	host.ProjectDir = taskStackProjectDir
	host.TaskID = "task-retry"
	host.AttemptID = "att-retry"
	host.IsHost = true
	host.State = domain.RunStateRunning
	host.StartedAt = attempt.StartedAt
	host.ActiveSteps = []string{"develop"}
	require.NoError(t, store.CreateRun(ctx, host))

	child := domain.NewRun("child-run-1", "develop")
	child.ProjectDir = taskStackProjectDir
	child.TaskID = "task-retry"
	child.AttemptID = "att-retry"
	child.ParentRunID = host.ID
	child.ParentStepName = "develop"
	child.State = domain.RunStateRunning
	child.StartedAt = now.Add(-7 * time.Minute)
	require.NoError(t, store.CreateRun(ctx, child))

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.Running, 1)
	entry := stack.Running[0]
	assert.Equal(t, "host-run-1", entry.RunID)
	assert.InDelta(t, 420, entry.ElapsedSeconds, 5, "elapsed must follow the retried child run's own start, not the host run's original start")
	assert.InDelta(t, 2400, entry.TotalElapsedSeconds, 5, "total elapsed must still reflect the task's first attempt")
}

// TestAPITaskStack_Running_ElapsedFollowsInRunStepRetry covers an in-DSL
// retry loop (e.g. develop.cloche's test:fail -> fix) that stays within a
// single run: the Running row's elapsed must reflect the currently executing
// step's own start (from step_executions), not the run's StartedAt from
// before the retry began.
func TestAPITaskStack_Running_ElapsedFollowsInRunStepRetry(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()
	now := time.Now()

	task := &domain.Task{ID: "task-steploop", Title: "Fix flaky test", Source: domain.TaskSourceExternal, ProjectDir: taskStackProjectDir, CreatedAt: now}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := &domain.Attempt{ID: "att-steploop", TaskID: "task-steploop", StartedAt: now.Add(-30 * time.Minute), Result: domain.AttemptResultRunning}
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	run := domain.NewRun("run-steploop", "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = "task-steploop"
	run.AttemptID = "att-steploop"
	run.State = domain.RunStateRunning
	run.StartedAt = attempt.StartedAt
	run.ActiveSteps = []string{"fix"}
	require.NoError(t, store.CreateRun(ctx, run))

	// The first step (test) started with the run and failed 20 minutes ago;
	// the retry step (fix) started 6 minutes ago and is still running.
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{StepName: "test", StartedAt: run.StartedAt}))
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{StepName: "test", Result: "fail", CompletedAt: now.Add(-20 * time.Minute)}))
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{StepName: "fix", StartedAt: now.Add(-6 * time.Minute)}))

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.Running, 1)
	entry := stack.Running[0]
	assert.Equal(t, "run-steploop", entry.RunID)
	assert.InDelta(t, 360, entry.ElapsedSeconds, 5, "elapsed must follow the currently running step's own start, not the run's original start")
	assert.InDelta(t, 1800, entry.TotalElapsedSeconds, 5, "total elapsed must still reflect the task's first attempt")
}

func TestAPITaskStack_Running_WaitingRunIncluded(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	task := &domain.Task{ID: "task-waiting", Title: "Poll for CI", Source: domain.TaskSourceExternal, ProjectDir: taskStackProjectDir, CreatedAt: time.Now()}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := &domain.Attempt{ID: "att-waiting", TaskID: "task-waiting", StartedAt: time.Now().Add(-5 * time.Minute), Result: domain.AttemptResultRunning}
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	run := domain.NewRun("run-waiting", "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = "task-waiting"
	run.AttemptID = "att-waiting"
	run.State = domain.RunStateWaiting
	run.StartedAt = attempt.StartedAt
	run.ActiveSteps = []string{"wait-for-ci"}
	require.NoError(t, store.CreateRun(ctx, run))

	pollStore, ok := ports.RunStore(store).(ports.PollStore)
	require.True(t, ok, "sqlite.Store must implement ports.PollStore")
	lastPollAt := time.Now().Add(-30 * time.Second)
	require.NoError(t, pollStore.UpsertPoll(ctx, &ports.PollRecord{
		RunID:      run.ID,
		StepName:   "wait-for-ci",
		StartedAt:  attempt.StartedAt,
		LastPollAt: lastPollAt,
		PollCount:  4,
	}))

	resp, stack := getTaskStack(t, h, "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	require.Len(t, stack.Running, 1)
	entry := stack.Running[0]
	assert.Equal(t, "task-waiting", entry.TaskID)
	assert.Equal(t, "run-waiting", entry.RunID)
	assert.Contains(t, entry.CurrentStep, "waiting")
	assert.Contains(t, entry.CurrentStep, "poll wait-for-ci")
	assert.Contains(t, entry.CurrentStep, "4 polls")
	assert.Empty(t, stack.Done)
}

func TestAPITaskStack_AttemptNumberSecondAttempt(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	task := &domain.Task{ID: "task-2", Title: "Flaky test", Source: domain.TaskSourceExternal, ProjectDir: taskStackProjectDir, CreatedAt: time.Now()}
	require.NoError(t, store.SaveTask(ctx, task))

	att1 := &domain.Attempt{ID: "att1", TaskID: "task-2", StartedAt: time.Now().Add(-10 * time.Minute), EndedAt: time.Now().Add(-8 * time.Minute), Result: domain.AttemptResultFailed}
	require.NoError(t, store.SaveAttempt(ctx, att1))
	att2 := &domain.Attempt{ID: "att2", TaskID: "task-2", StartedAt: time.Now().Add(-1 * time.Minute), Result: domain.AttemptResultRunning, PreviousAttemptID: "att1"}
	require.NoError(t, store.SaveAttempt(ctx, att2))

	run1 := domain.NewRun("run-1", "develop")
	run1.ProjectDir = taskStackProjectDir
	run1.TaskID = "task-2"
	run1.AttemptID = "att1"
	run1.State = domain.RunStateFailed
	run1.StartedAt = att1.StartedAt
	run1.CompletedAt = att1.EndedAt
	require.NoError(t, store.CreateRun(ctx, run1))

	run2 := domain.NewRun("run-2", "develop")
	run2.ProjectDir = taskStackProjectDir
	run2.TaskID = "task-2"
	run2.AttemptID = "att2"
	run2.State = domain.RunStateRunning
	run2.StartedAt = att2.StartedAt
	require.NoError(t, store.CreateRun(ctx, run2))

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.Running, 1)
	assert.Equal(t, "run-2", stack.Running[0].RunID)
	assert.Equal(t, 2, stack.Running[0].Attempt)
	assert.Empty(t, stack.Done, "the active attempt supersedes the failed one for grouping purposes")
}

func TestAPITaskStack_AdHocRunIsSyntheticSingleAttempt(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("adhoc-run-1", "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = ""
	run.State = domain.RunStateRunning
	run.StartedAt = time.Now().Add(-30 * time.Second)
	require.NoError(t, store.CreateRun(ctx, run))

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.Running, 1)
	assert.Equal(t, "", stack.Running[0].TaskID)
	assert.Equal(t, "adhoc-run-1", stack.Running[0].RunID)
	assert.Equal(t, 1, stack.Running[0].Attempt)
}

func TestAPITaskStack_Done(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	task := &domain.Task{ID: "task-3", Title: "Ship it", Source: domain.TaskSourceExternal, ProjectDir: taskStackProjectDir, CreatedAt: time.Now()}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := &domain.Attempt{ID: "att3", TaskID: "task-3", StartedAt: time.Now().Add(-5 * time.Minute), EndedAt: time.Now(), Result: domain.AttemptResultSucceeded}
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	run := domain.NewRun("run-3", "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = "task-3"
	run.AttemptID = "att3"
	run.State = domain.RunStateSucceeded
	run.StartedAt = attempt.StartedAt
	run.CompletedAt = attempt.EndedAt
	require.NoError(t, store.CreateRun(ctx, run))

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.Done, 1)
	entry := stack.Done[0]
	assert.Equal(t, "task-3", entry.TaskID)
	assert.Equal(t, "Ship it", entry.Title)
	assert.Equal(t, "succeeded", entry.Outcome)
	assert.InDelta(t, 300, entry.DurationSeconds, 2)
	assert.Empty(t, stack.Running)
}

// TestAPITaskStack_Done_NoAgeBoundary confirms the Done group is no longer
// restricted to "today": a task completed several days ago still shows up on
// the first page (superseding cloche-eiil.24, the UTC-midnight bug — there's
// no midnight boundary left to get wrong).
func TestAPITaskStack_Done_NoAgeBoundary(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	old := domain.NewRun("run-old", "develop")
	old.ProjectDir = taskStackProjectDir
	old.TaskID = "task-old"
	old.State = domain.RunStateSucceeded
	old.StartedAt = time.Now().Add(-96 * time.Hour)
	old.CompletedAt = time.Now().Add(-95 * time.Hour)
	require.NoError(t, store.CreateRun(ctx, old))

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.Done, 1)
	assert.Equal(t, "task-old", stack.Done[0].TaskID)
}

func TestAPITaskStack_ExcludesUserInitiatedBuiltinRun(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	// Manual `cloche run intent-scan`: user-initiated, title doesn't match
	// the auto-trigger title, so it should be excluded entirely.
	manualTask := &domain.Task{ID: "user-manual1", Title: "manual scan", Source: domain.TaskSourceUserInitiated, ProjectDir: taskStackProjectDir, CreatedAt: time.Now()}
	require.NoError(t, store.SaveTask(ctx, manualTask))
	manualRun := domain.NewRun("run-manual", "intent-scan")
	manualRun.ProjectDir = taskStackProjectDir
	manualRun.TaskID = "user-manual1"
	manualRun.IsHost = true
	manualRun.State = domain.RunStateRunning
	manualRun.StartedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, manualRun))

	// Auto-triggered post-task intent-scan: also user-initiated (no issue
	// ID), but its title matches builtin.AutoTriggerTitles, so it should
	// still appear.
	autoTask := &domain.Task{ID: "user-auto1", Title: "Incremental intent scan (post-task)", Source: domain.TaskSourceUserInitiated, ProjectDir: taskStackProjectDir, CreatedAt: time.Now()}
	require.NoError(t, store.SaveTask(ctx, autoTask))
	autoRun := domain.NewRun("run-auto", "intent-scan")
	autoRun.ProjectDir = taskStackProjectDir
	autoRun.TaskID = "user-auto1"
	autoRun.IsHost = true
	autoRun.State = domain.RunStateRunning
	autoRun.StartedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, autoRun))

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.Running, 1)
	assert.Equal(t, "run-auto", stack.Running[0].RunID)
}

// TestAPITaskStack_ExcludesUserInitiatedBuiltinRun_Done covers the Done
// side of the same exclusion: a manually triggered builtin run that has
// since completed must stay out of Done, not just Running.
func TestAPITaskStack_ExcludesUserInitiatedBuiltinRun_Done(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	manualTask := &domain.Task{ID: "user-manual2", Title: "manual scan", Source: domain.TaskSourceUserInitiated, ProjectDir: taskStackProjectDir, CreatedAt: time.Now()}
	require.NoError(t, store.SaveTask(ctx, manualTask))
	manualRun := domain.NewRun("run-manual-done", "intent-scan")
	manualRun.ProjectDir = taskStackProjectDir
	manualRun.TaskID = "user-manual2"
	manualRun.IsHost = true
	manualRun.State = domain.RunStateSucceeded
	manualRun.StartedAt = time.Now().Add(-time.Minute)
	manualRun.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, manualRun))

	_, stack := getTaskStack(t, h, "")
	assert.Empty(t, stack.Done, "a completed manual builtin run must stay excluded from Done")
}

func TestAPITaskStack_Queued(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// resolveProjectDir only resolves dirs that have at least one run.
	seed := domain.NewRun("seed", "develop")
	seed.ProjectDir = taskStackProjectDir
	seed.State = domain.RunStateSucceeded
	seed.StartedAt = time.Now()
	seed.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, seed))

	h.occupancyProvider = &fakeOccupancyProvider{
		snapshots: map[string]LoopOccupancy{
			taskStackProjectDir: {
				Queued: []QueuedItem{{TaskID: "task-q", Reason: "capacity", Since: "2026-01-01T00:00:00Z"}},
			},
		},
	}

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.Queued, 1)
	assert.Equal(t, "task-q", stack.Queued[0].TaskID)
	assert.Equal(t, "capacity", stack.Queued[0].Reason)
}

func TestAPITaskStack_NeedsYou(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	seed := domain.NewRun("seed", "develop")
	seed.ProjectDir = taskStackProjectDir
	seed.State = domain.RunStateSucceeded
	seed.StartedAt = time.Now()
	seed.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, seed))

	since := time.Now().Add(-time.Hour)
	h.attentionProvider = &mockAttentionProvider{
		items: map[string][]attention.Item{
			taskStackProjectDir: {
				{Kind: attention.KindParked, ProjectDir: taskStackProjectDir, TaskID: "task-p", RunID: "run-p", Reason: "parked", Since: since, Actions: []string{"reply"}},
			},
		},
	}

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.NeedsYou, 1)
	assert.Equal(t, "parked", string(stack.NeedsYou[0].Kind))
	assert.Equal(t, "task-p", stack.NeedsYou[0].TaskID)
	assert.Equal(t, []string{"reply"}, stack.NeedsYou[0].Actions)
	// KindParked isn't one of the kinds the close action applies to, so
	// CloseAvailable stays false regardless of the project's workflows.
	assert.False(t, stack.NeedsYou[0].CloseAvailable)
}

func TestAPITaskStack_NeedsYou_CloseAvailableForRepeatFailure(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	seed := domain.NewRun("seed", "develop")
	seed.ProjectDir = taskStackProjectDir
	seed.State = domain.RunStateSucceeded
	seed.StartedAt = time.Now()
	seed.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, seed))

	since := time.Now().Add(-time.Hour)
	h.attentionProvider = &mockAttentionProvider{
		items: map[string][]attention.Item{
			taskStackProjectDir: {
				{Kind: attention.KindRepeatFailure, ProjectDir: taskStackProjectDir, TaskID: "task-r", RunID: "run-r", Reason: "3 failures", Since: since, Actions: []string{"release", "close", "run-once"}, Key: "repeat-failure:task-r"},
			},
		},
	}

	// No .cloche directory exists at taskStackProjectDir, so no close/cancel
	// contract is defined — CloseAvailable must be false.
	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.NeedsYou, 1)
	assert.Equal(t, "repeat-failure:task-r", stack.NeedsYou[0].Key)
	assert.False(t, stack.NeedsYou[0].CloseAvailable)
}

func TestAPITaskStack_NeedsYou_CloseAvailableWhenContractDefined(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	projectDir := t.TempDir()
	slug := filepath.Base(projectDir)
	clocheDir := filepath.Join(projectDir, ".cloche")
	require.NoError(t, os.MkdirAll(clocheDir, 0755))
	content := `workflow close-task {
  host {}
  step close-task {
    run     = "echo closed"
    results = [success, fail]
  }
  close-task:success -> done
  close-task:fail    -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(clocheDir, "host.cloche"), []byte(content), 0644))

	seed := domain.NewRun("seed-close", "develop")
	seed.ProjectDir = projectDir
	seed.State = domain.RunStateSucceeded
	seed.StartedAt = time.Now()
	seed.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, seed))

	since := time.Now().Add(-time.Hour)
	h.attentionProvider = &mockAttentionProvider{
		items: map[string][]attention.Item{
			projectDir: {
				{Kind: attention.KindRepeatFailure, ProjectDir: projectDir, TaskID: "task-r", RunID: "run-r", Reason: "3 failures", Since: since, Actions: []string{"release", "close", "run-once"}, Key: "repeat-failure:task-r"},
			},
		},
	}

	req := httptest.NewRequest("GET", "/api/projects/"+slug+"/tasks/stack", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var stack TaskStack
	require.NoError(t, json.NewDecoder(w.Body).Decode(&stack))
	require.Len(t, stack.NeedsYou, 1)
	assert.True(t, stack.NeedsYou[0].CloseAvailable)
}

func TestAPITaskStack_NoProvidersConfigured(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	seed := domain.NewRun("seed", "develop")
	seed.ProjectDir = taskStackProjectDir
	seed.State = domain.RunStateSucceeded
	seed.StartedAt = time.Now().Add(-time.Minute)
	seed.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, seed))

	_, stack := getTaskStack(t, h, "")
	assert.NotNil(t, stack.NeedsYou)
	assert.NotNil(t, stack.Queued)
	assert.NotNil(t, stack.Running)
	assert.NotNil(t, stack.Done)
}

func TestAPITaskStack_ProjectNotFound(t *testing.T) {
	h, _ := setupHandler(t)
	req := httptest.NewRequest("GET", "/api/projects/nonexistent/tasks/stack", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPITaskStack_InvalidCursor(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()
	seed := domain.NewRun("seed", "develop")
	seed.ProjectDir = taskStackProjectDir
	seed.State = domain.RunStateSucceeded
	seed.StartedAt = time.Now()
	seed.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, seed))

	resp, _ := getTaskStack(t, h, "?cursor=not-valid-base64!!")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAPITaskStack_ETagNotModified(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = "task-1"
	run.State = domain.RunStateSucceeded
	run.StartedAt = time.Now()
	run.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, run))

	resp1, _ := getTaskStack(t, h, "")
	require.Equal(t, http.StatusOK, resp1.StatusCode)
	etag := resp1.Header.Get("ETag")
	require.NotEmpty(t, etag)

	req := httptest.NewRequest("GET", "/api/projects/stackapp/tasks/stack", nil)
	req.Header.Set("If-None-Match", etag)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotModified, w.Code)
	assert.Empty(t, w.Body.Bytes())

	// A genuine state change (new run) changes the ETag.
	run2 := domain.NewRun("run-2", "develop")
	run2.ProjectDir = taskStackProjectDir
	run2.TaskID = "task-2"
	run2.State = domain.RunStateRunning
	run2.StartedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, run2))

	resp2, _ := getTaskStack(t, h, "")
	assert.NotEqual(t, etag, resp2.Header.Get("ETag"))
}

func TestStackForETag_IgnoresElapsedSeconds(t *testing.T) {
	base := &TaskStack{
		Running: []TaskStackRunning{{TaskID: "t1", RunID: "r1", Attempt: 1, CurrentStep: "build", ElapsedSeconds: 5}},
	}
	later := &TaskStack{
		Running: []TaskStackRunning{{TaskID: "t1", RunID: "r1", Attempt: 1, CurrentStep: "build", ElapsedSeconds: 500}},
	}

	baseBody, err := json.Marshal(stackForETag(base))
	require.NoError(t, err)
	laterBody, err := json.Marshal(stackForETag(later))
	require.NoError(t, err)

	assert.Equal(t, taskStackETag(baseBody), taskStackETag(laterBody))

	changed := &TaskStack{
		Running: []TaskStackRunning{{TaskID: "t1", RunID: "r2", Attempt: 1, CurrentStep: "build", ElapsedSeconds: 5}},
	}
	changedBody, err := json.Marshal(stackForETag(changed))
	require.NoError(t, err)
	assert.NotEqual(t, taskStackETag(baseBody), taskStackETag(changedBody))
}

// seedDoneRun creates a top-level, terminal run for taskStackProjectDir with
// the given task ID and completion time, one task per run (no retries).
func seedDoneRun(t *testing.T, store *sqlite.Store, id, taskID string, completedAt time.Time) {
	t.Helper()
	run := domain.NewRun(id, "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = taskID
	run.State = domain.RunStateSucceeded
	run.StartedAt = completedAt.Add(-time.Minute)
	run.CompletedAt = completedAt
	require.NoError(t, store.CreateRun(context.Background(), run))
}

func TestAPITaskStack_DonePagesNewestFirstRegardlessOfAge(t *testing.T) {
	h, store := setupHandler(t)
	now := time.Now()

	// 30 completions spread across several days — old enough that a
	// "today" boundary would have hidden most of them.
	for i := 0; i < 30; i++ {
		seedDoneRun(t, store, fmt.Sprintf("run-%02d", i), fmt.Sprintf("task-%02d", i), now.Add(-time.Duration(i)*6*time.Hour))
	}

	_, page1 := getTaskStack(t, h, "")
	assert.Len(t, page1.Done, defaultDonePageSize)
	require.NotEmpty(t, page1.Cursor)
	for i, entry := range page1.Done {
		assert.Equal(t, fmt.Sprintf("task-%02d", i), entry.TaskID, "page 1 must be newest-first with no age cutoff")
	}

	_, page2 := getTaskStack(t, h, "?cursor="+page1.Cursor)
	assert.Len(t, page2.Done, 30-defaultDonePageSize)
	assert.Empty(t, page2.Cursor, "all done entries fit in two pages")
	for i, entry := range page2.Done {
		assert.Equal(t, fmt.Sprintf("task-%02d", defaultDonePageSize+i), entry.TaskID)
	}
}

func TestAPITaskStack_DonePageSizeParam(t *testing.T) {
	h, store := setupHandler(t)
	now := time.Now()
	for i := 0; i < 10; i++ {
		seedDoneRun(t, store, fmt.Sprintf("run-%02d", i), fmt.Sprintf("task-%02d", i), now.Add(-time.Duration(i)*time.Minute))
	}

	_, small := getTaskStack(t, h, "?page_size=3")
	assert.Len(t, small.Done, 3)
	require.NotEmpty(t, small.Cursor)

	_, all := getTaskStack(t, h, "?page_size=100")
	assert.Len(t, all.Done, 10)
	assert.Empty(t, all.Cursor)

	resp, _ := getTaskStack(t, h, "?page_size=notanumber")
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
}

func TestAPITaskStack_DonePageSizeClampedToMax(t *testing.T) {
	h, store := setupHandler(t)
	now := time.Now()
	for i := 0; i < maxDonePageSize+10; i++ {
		seedDoneRun(t, store, fmt.Sprintf("run-%03d", i), fmt.Sprintf("task-%03d", i), now.Add(-time.Duration(i)*time.Minute))
	}

	_, stack := getTaskStack(t, h, "?page_size=100000")
	assert.Len(t, stack.Done, maxDonePageSize, "page_size above the max is clamped rather than rejected or unbounded")
	assert.NotEmpty(t, stack.Cursor)
}

// TestAPITaskStack_DoneCursorStableOnTies covers the "completed_at + task
// id" half of the cursor: two entries sharing an exact completion timestamp
// must each appear exactly once across pages, and repeating the same
// (no-cursor) request must always return the same first page.
func TestAPITaskStack_DoneCursorStableOnTies(t *testing.T) {
	h, store := setupHandler(t)
	tie := time.Now()
	seedDoneRun(t, store, "run-a", "task-a", tie)
	seedDoneRun(t, store, "run-b", "task-b", tie)

	_, first := getTaskStack(t, h, "?page_size=1")
	require.Len(t, first.Done, 1)
	require.NotEmpty(t, first.Cursor)

	// Re-running the same first-page request must be deterministic.
	_, firstAgain := getTaskStack(t, h, "?page_size=1")
	require.Len(t, firstAgain.Done, 1)
	assert.Equal(t, first.Done[0].TaskID, firstAgain.Done[0].TaskID, "first page must be stable across repeated requests")
	assert.Equal(t, first.Cursor, firstAgain.Cursor)

	_, second := getTaskStack(t, h, "?page_size=1&cursor="+first.Cursor)
	require.Len(t, second.Done, 1)
	assert.Empty(t, second.Cursor)
	assert.NotEqual(t, first.Done[0].TaskID, second.Done[0].TaskID, "the tied entry must not repeat on the next page")

	seen := map[string]bool{first.Done[0].TaskID: true, second.Done[0].TaskID: true}
	assert.True(t, seen["task-a"] && seen["task-b"], "both tied entries must be reachable, exactly once, across the two pages")
}

// TestAPITaskStack_DoneDedupesRetriesToLatestAttempt covers the bounded
// query's dedup: a task with two terminal attempts (a failed retry followed
// by a later success) must appear once in Done, as its latest attempt.
func TestAPITaskStack_DoneDedupesRetriesToLatestAttempt(t *testing.T) {
	h, store := setupHandler(t)
	now := time.Now()
	seedDoneRun(t, store, "run-1-fail", "task-retried", now.Add(-time.Hour))
	run2 := domain.NewRun("run-2-succeed", "develop")
	run2.ProjectDir = taskStackProjectDir
	run2.TaskID = "task-retried"
	run2.State = domain.RunStateSucceeded
	run2.StartedAt = now.Add(-time.Minute)
	run2.CompletedAt = now
	require.NoError(t, store.CreateRun(context.Background(), run2))

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.Done, 1)
	assert.Equal(t, "run-2-succeed", stack.Done[0].RunID, "only the latest terminal attempt should appear")
}

func TestEncodeDecodeTaskStackCursor_RoundTrip(t *testing.T) {
	want := doneCursor{CompletedAt: time.Now().UTC().Truncate(time.Nanosecond), TaskKey: "task-xyz"}
	encoded := encodeTaskStackCursor(want)
	got, has, err := decodeTaskStackCursor(encoded)
	require.NoError(t, err)
	require.True(t, has)
	assert.True(t, want.CompletedAt.Equal(got.CompletedAt))
	assert.Equal(t, want.TaskKey, got.TaskKey)
}

func TestDecodeTaskStackCursor_Invalid(t *testing.T) {
	_, _, err := decodeTaskStackCursor("not-base64!!!")
	assert.Error(t, err)
}

func TestDecodeTaskStackCursor_Empty(t *testing.T) {
	_, has, err := decodeTaskStackCursor("")
	require.NoError(t, err)
	assert.False(t, has)
}

// TestAPITaskStack_RepoScoping covers a multi-repo project end to end: every
// group carries a Repository, ?repo=<name> scopes each group to it, ?repo=all
// (and no repo param at all) is the merged view, and RepoCounts always
// reflects the full, unscoped picture regardless of the current scope — see
// docs/design/console-repo-grouping-mock.html, option 1.
func TestAPITaskStack_RepoScoping(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()
	projectDir := t.TempDir()
	slug := filepath.Base(projectDir)

	runningManager := domain.NewRun("run-manager-running", "develop")
	runningManager.ProjectDir = projectDir
	runningManager.TaskID = "task-manager-running"
	runningManager.State = domain.RunStateRunning
	runningManager.StartedAt = time.Now().Add(-time.Minute)
	runningManager.Repository = "manager"
	require.NoError(t, store.CreateRun(ctx, runningManager))

	runningAnarkana := domain.NewRun("run-anarkana-running", "develop")
	runningAnarkana.ProjectDir = projectDir
	runningAnarkana.TaskID = "task-anarkana-running"
	runningAnarkana.State = domain.RunStateRunning
	runningAnarkana.StartedAt = time.Now().Add(-time.Minute)
	runningAnarkana.Repository = "anarkana"
	require.NoError(t, store.CreateRun(ctx, runningAnarkana))

	doneManager := domain.NewRun("run-manager-done", "develop")
	doneManager.ProjectDir = projectDir
	doneManager.TaskID = "task-manager-done"
	doneManager.State = domain.RunStateSucceeded
	doneManager.StartedAt = time.Now().Add(-time.Hour)
	doneManager.CompletedAt = time.Now().Add(-time.Minute)
	doneManager.Repository = "manager"
	require.NoError(t, store.CreateRun(ctx, doneManager))

	needsYouRun := domain.NewRun("run-anarkana-needsyou", "develop")
	needsYouRun.ProjectDir = projectDir
	needsYouRun.TaskID = "task-anarkana-needsyou"
	needsYouRun.State = domain.RunStateFailed
	needsYouRun.Repository = "anarkana"
	require.NoError(t, store.CreateRun(ctx, needsYouRun))

	// Parked is neither an active state (so this doesn't leak into Running)
	// nor a terminal state ListDoneRunsByProject queries for (so it doesn't
	// leak into Done either) — it exists purely so repositoryForRun's
	// RunID -> Repository lookup for the queued item below has something
	// real to resolve, mirroring how a "resuming" queued item references a
	// genuine run without this test needing to model concurrency slots.
	queuedRun := domain.NewRun("run-manager-queued", "develop")
	queuedRun.ProjectDir = projectDir
	queuedRun.TaskID = "task-manager-queued"
	queuedRun.State = domain.RunStateParked
	queuedRun.Repository = "manager"
	require.NoError(t, store.CreateRun(ctx, queuedRun))

	h.attentionProvider = &mockAttentionProvider{
		items: map[string][]attention.Item{
			projectDir: {
				{Kind: attention.KindRepeatFailure, ProjectDir: projectDir, TaskID: "task-anarkana-needsyou", RunID: "run-anarkana-needsyou", Reason: "3 failures", Since: time.Now()},
			},
		},
	}
	h.occupancyProvider = &fakeOccupancyProvider{
		snapshots: map[string]LoopOccupancy{
			projectDir: {
				Queued: []QueuedItem{{TaskID: "task-manager-queued", RunID: "run-manager-queued", Reason: "capacity", Since: "2026-01-01T00:00:00Z"}},
			},
		},
	}

	getStack := func(query string) TaskStack {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/projects/"+slug+"/tasks/stack"+query, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		var stack TaskStack
		require.NoError(t, json.NewDecoder(w.Body).Decode(&stack))
		return stack
	}

	// Unscoped ("all repos"): every group holds both repos' entries, and
	// every entry carries its Repository.
	all := getStack("")

	require.Len(t, all.Running, 2)
	require.Len(t, all.NeedsYou, 1)
	require.Len(t, all.Queued, 1)
	require.Len(t, all.Done, 1)
	assert.Equal(t, "anarkana", all.NeedsYou[0].Repository)
	assert.Equal(t, "manager", all.Queued[0].Repository)
	assert.Equal(t, "manager", all.Done[0].Repository)

	require.Contains(t, all.RepoCounts, "manager")
	require.Contains(t, all.RepoCounts, "anarkana")
	assert.Equal(t, TaskStackRepoCounts{NeedsYou: 0, Running: 1, Queued: 1, Done: 1}, all.RepoCounts["manager"])
	assert.Equal(t, TaskStackRepoCounts{NeedsYou: 1, Running: 1, Queued: 0, Done: 0}, all.RepoCounts["anarkana"])

	// ?repo=all is a synonym for the same merged view.
	allAgain := getStack("?repo=all")
	assert.Equal(t, len(all.Running), len(allAgain.Running))

	// Scoped to "manager": only manager's entries appear in every group, but
	// RepoCounts is unchanged (still describes both repos).
	manager := getStack("?repo=manager")
	require.Len(t, manager.Running, 1)
	assert.Equal(t, "task-manager-running", manager.Running[0].TaskID)
	assert.Equal(t, "manager", manager.Running[0].Repository)
	assert.Empty(t, manager.NeedsYou, "anarkana's needs-you item must not leak into the manager-scoped view")
	require.Len(t, manager.Queued, 1)
	assert.Equal(t, "task-manager-queued", manager.Queued[0].TaskID)
	require.Len(t, manager.Done, 1)
	assert.Equal(t, "task-manager-done", manager.Done[0].TaskID)
	assert.Equal(t, all.RepoCounts, manager.RepoCounts)

	// Scoped to "anarkana": only anarkana's entries appear.
	anarkana := getStack("?repo=anarkana")
	require.Len(t, anarkana.Running, 1)
	assert.Equal(t, "task-anarkana-running", anarkana.Running[0].TaskID)
	require.Len(t, anarkana.NeedsYou, 1)
	assert.Equal(t, "task-anarkana-needsyou", anarkana.NeedsYou[0].TaskID)
	assert.Empty(t, anarkana.Queued)
	assert.Empty(t, anarkana.Done)
}

// TestAPITaskStack_LegacyProjectOmitsRepositoryFields asserts that a legacy
// (single/no-repo) project's stack response is byte-shape-identical to what
// it was before this feature on the fields that matter: every entry's
// "repository" key is absent (empty + omitempty) rather than
// present-and-blank. (RepoCounts itself still comes back — a map keyed by
// "" for these unassigned runs — but the console never looks at it for a
// project whose /api/projects entry declares one or zero repositories, so
// its presence is harmless.)
func TestAPITaskStack_LegacyProjectOmitsRepositoryFields(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-legacy", "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = "task-legacy"
	run.State = domain.RunStateRunning
	run.StartedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, run))

	seedDoneRun(t, store, "run-legacy-done", "task-legacy-done", time.Now())

	resp, err := http.NewRequest("GET", "/api/projects/stackapp/tasks/stack", nil)
	require.NoError(t, err)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, resp)
	require.Equal(t, http.StatusOK, w.Code)

	var raw map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &raw))

	var runningRaw []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["running"], &runningRaw))
	require.Len(t, runningRaw, 1)
	_, hasRepo := runningRaw[0]["repository"]
	assert.False(t, hasRepo, "legacy running entries must omit \"repository\" entirely")

	var doneRaw []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw["done"], &doneRaw))
	require.Len(t, doneRaw, 1)
	_, hasDoneRepo := doneRaw[0]["repository"]
	assert.False(t, hasDoneRepo, "legacy done entries must omit \"repository\" entirely")
}

// TestAPITaskStack_RepoScoping_MultiRepoAndUnattributed reproduces the
// wrapped_cloche bug report: a host workflow that declares no repos (or more
// than one) previously left every one of its runs with an empty
// run.Repository, so they only ever showed up under "all repos" and never
// under the repo-specific sub-tab (?repo=cloche). With richer attribution
// (domain.Run.Repositories, resolved by domain.ResolveRunRepositories) such
// a run can be attributed to more than one repo's sub-tab at once, and a run
// that still resolves to nothing is counted separately as unattributed
// rather than silently vanishing from every named sub-tab.
func TestAPITaskStack_RepoScoping_MultiRepoAndUnattributed(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()
	projectDir := t.TempDir()
	slug := filepath.Base(projectDir)

	// A host workflow run belonging to more than one repo (rule d/c of
	// domain.ResolveRunRepositories) — e.g. wrapped_cloche's "main" touching
	// both repos/cloche and docs.
	multiRepoRun := domain.NewRun("run-multi-repo", "main")
	multiRepoRun.ProjectDir = projectDir
	multiRepoRun.TaskID = "task-multi-repo"
	multiRepoRun.State = domain.RunStateSucceeded
	multiRepoRun.StartedAt = time.Now().Add(-time.Hour)
	multiRepoRun.CompletedAt = time.Now().Add(-time.Minute)
	multiRepoRun.Repositories = []string{"cloche", "docs"}
	require.NoError(t, store.CreateRun(ctx, multiRepoRun))

	// A run that resolves to no repository at all — must stay visible under
	// "all repos" and be counted as unattributed, not dropped.
	unattributedRun := domain.NewRun("run-unattributed", "main")
	unattributedRun.ProjectDir = projectDir
	unattributedRun.TaskID = "task-unattributed"
	unattributedRun.State = domain.RunStateSucceeded
	unattributedRun.StartedAt = time.Now().Add(-time.Hour)
	unattributedRun.CompletedAt = time.Now().Add(-2 * time.Minute)
	require.NoError(t, store.CreateRun(ctx, unattributedRun))

	getStack := func(query string) TaskStack {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/projects/"+slug+"/tasks/stack"+query, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)
		var stack TaskStack
		require.NoError(t, json.NewDecoder(w.Body).Decode(&stack))
		return stack
	}

	all := getStack("")
	require.Len(t, all.Done, 2, "both runs remain visible under all repos")

	// The bug this reproduces: ?repo=cloche must return the multi-repo run.
	cloche := getStack("?repo=cloche")
	require.Len(t, cloche.Done, 1)
	assert.Equal(t, "task-multi-repo", cloche.Done[0].TaskID)

	docs := getStack("?repo=docs")
	require.Len(t, docs.Done, 1)
	assert.Equal(t, "task-multi-repo", docs.Done[0].TaskID, "a multi-repo run appears under every repo it belongs to")

	// The unattributed run must not leak into either named sub-tab.
	assert.NotContains(t, []string{cloche.Done[0].TaskID}, "task-unattributed")
	assert.NotContains(t, []string{docs.Done[0].TaskID}, "task-unattributed")

	require.Contains(t, all.RepoCounts, "cloche")
	require.Contains(t, all.RepoCounts, "docs")
	require.Contains(t, all.RepoCounts, "", "unattributed runs are counted under the \"\" bucket")
	assert.Equal(t, 1, all.RepoCounts["cloche"].Done)
	assert.Equal(t, 1, all.RepoCounts["docs"].Done)
	assert.Equal(t, 1, all.RepoCounts[""].Done, "the unattributed run is counted once, separately from either named repo")
}
