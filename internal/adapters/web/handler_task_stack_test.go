package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/attention"
	"github.com/swordsmanluke/cloche/internal/domain"
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
	assert.Empty(t, stack.DoneToday)
	assert.Empty(t, stack.Queued)
	assert.Empty(t, stack.NeedsYou)
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
	assert.Empty(t, stack.DoneToday, "the active attempt supersedes the failed one for grouping purposes")
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

func TestAPITaskStack_DoneToday(t *testing.T) {
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
	require.Len(t, stack.DoneToday, 1)
	entry := stack.DoneToday[0]
	assert.Equal(t, "task-3", entry.TaskID)
	assert.Equal(t, "Ship it", entry.Title)
	assert.Equal(t, "succeeded", entry.Outcome)
	assert.InDelta(t, 300, entry.DurationSeconds, 2)
	assert.Empty(t, stack.Running)
}

func TestAPITaskStack_DoneTodayDate(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	seed := domain.NewRun("seed", "develop")
	seed.ProjectDir = taskStackProjectDir
	seed.State = domain.RunStateSucceeded
	seed.StartedAt = time.Now()
	seed.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, seed))

	_, stack := getTaskStack(t, h, "")
	assert.Equal(t, doneTodayDateLabel(time.Now()), stack.DoneTodayDate)
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
	assert.NotNil(t, stack.DoneToday)
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

func TestPaginateDone_CapsAndPagesByCursor(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	startOfToday := time.Date(2026, 6, 15, 0, 0, 0, 0, time.UTC)

	var candidates []doneCandidate
	// 25 completions earlier today (newest first as paginateDone expects).
	for i := 0; i < 25; i++ {
		completedAt := now.Add(-time.Duration(i) * time.Minute)
		candidates = append(candidates, doneCandidate{
			entry:       TaskStackDone{TaskID: "today", RunID: "r"},
			completedAt: completedAt,
		})
	}
	// 3 completions from yesterday.
	for i := 0; i < 3; i++ {
		completedAt := startOfToday.Add(-time.Duration(i+1) * time.Hour)
		candidates = append(candidates, doneCandidate{
			entry:       TaskStackDone{TaskID: "yesterday", RunID: "r"},
			completedAt: completedAt,
		})
	}

	page, cursor := paginateDone(candidates, time.Time{}, false, now)
	assert.Len(t, page, taskStackDoneCap)
	require.NotEmpty(t, cursor)

	cursorTime, hasCursor, err := decodeTaskStackCursor(cursor)
	require.NoError(t, err)
	require.True(t, hasCursor)

	page2, cursor2 := paginateDone(candidates, cursorTime, true, now)
	// 5 remaining today entries + 3 from yesterday = 8, under the cap, so no further cursor.
	assert.Len(t, page2, 8)
	assert.Empty(t, cursor2)
	for _, e := range page2[:5] {
		assert.Equal(t, "today", e.TaskID)
	}
	for _, e := range page2[5:] {
		assert.Equal(t, "yesterday", e.TaskID)
	}
}

func TestPaginateDone_NoCompletionsTodayOffersCursorToOlderHistory(t *testing.T) {
	now := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	yesterday := now.Add(-24 * time.Hour)

	candidates := []doneCandidate{
		{entry: TaskStackDone{TaskID: "old", RunID: "r"}, completedAt: yesterday},
	}

	page, cursor := paginateDone(candidates, time.Time{}, false, now)
	assert.Empty(t, page)
	require.NotEmpty(t, cursor)

	cursorTime, hasCursor, err := decodeTaskStackCursor(cursor)
	require.NoError(t, err)
	require.True(t, hasCursor)
	page2, cursor2 := paginateDone(candidates, cursorTime, true, now)
	require.Len(t, page2, 1)
	assert.Equal(t, "old", page2[0].TaskID)
	assert.Empty(t, cursor2)
}

func TestPaginateDone_UsesDaemonLocalDayNotUTC(t *testing.T) {
	// 01:00 UTC on 16 Sep is 18:00 PDT on 15 Sep — the daemon's local
	// "today" hasn't rolled over yet even though UTC's has.
	pdt := time.FixedZone("PDT", -7*3600)
	now := time.Date(2026, 9, 16, 1, 0, 0, 0, time.UTC).In(pdt)

	completedAt := time.Date(2026, 9, 15, 15, 0, 0, 0, pdt) // 3pm PDT, same local day as now
	candidates := []doneCandidate{
		{entry: TaskStackDone{TaskID: "afternoon-pdt", RunID: "r"}, completedAt: completedAt},
	}

	page, cursor := paginateDone(candidates, time.Time{}, false, now)
	require.Len(t, page, 1, "a task completed earlier the same local day must appear in today's group")
	assert.Equal(t, "afternoon-pdt", page[0].TaskID)
	assert.Empty(t, cursor)
}

func TestDoneTodayDateLabel_UsesGivenZone(t *testing.T) {
	pdt := time.FixedZone("PDT", -7*3600)
	now := time.Date(2026, 9, 16, 1, 0, 0, 0, time.UTC).In(pdt)
	assert.Equal(t, "15 Sep", doneTodayDateLabel(now))
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

// TestAPITaskStack_OldDoneRunExcludedFromDefaultView verifies buildTaskStack
// bounds its run window to the current day: a run that completed well before
// today must not appear in the no-cursor (default) response.
func TestAPITaskStack_OldDoneRunExcludedFromDefaultView(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	old := time.Now().Add(-36 * time.Hour)
	task := &domain.Task{ID: "task-old", Title: "Old task", Source: domain.TaskSourceExternal, ProjectDir: taskStackProjectDir, CreatedAt: old}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := &domain.Attempt{ID: "att-old", TaskID: "task-old", StartedAt: old, EndedAt: old, Result: domain.AttemptResultSucceeded}
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	run := domain.NewRun("run-old", "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = "task-old"
	run.AttemptID = "att-old"
	run.State = domain.RunStateSucceeded
	run.StartedAt = old
	run.CompletedAt = old
	require.NoError(t, store.CreateRun(ctx, run))

	_, stack := getTaskStack(t, h, "")
	assert.Empty(t, stack.DoneToday)
}

// TestAPITaskStack_CursorPagesOlderDoneRuns verifies that a run outside the
// default (today-only) window is still reachable by paging the Done group
// via the cursor, and that its task's title is resolved correctly even
// though the task wasn't referenced by any run in the initial page's window.
func TestAPITaskStack_CursorPagesOlderDoneRuns(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	old := time.Now().Add(-36 * time.Hour)
	task := &domain.Task{ID: "task-old2", Title: "Old task 2", Source: domain.TaskSourceExternal, ProjectDir: taskStackProjectDir, CreatedAt: old}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := &domain.Attempt{ID: "att-old2", TaskID: "task-old2", StartedAt: old, EndedAt: old, Result: domain.AttemptResultSucceeded}
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	run := domain.NewRun("run-old2", "develop")
	run.ProjectDir = taskStackProjectDir
	run.TaskID = "task-old2"
	run.AttemptID = "att-old2"
	run.State = domain.RunStateSucceeded
	run.StartedAt = old
	run.CompletedAt = old
	require.NoError(t, store.CreateRun(ctx, run))

	_, first := getTaskStack(t, h, "")
	require.Empty(t, first.DoneToday)
	require.NotEmpty(t, first.Cursor, "expected a cursor into older history")

	_, second := getTaskStack(t, h, "?cursor="+first.Cursor)
	require.Len(t, second.DoneToday, 1)
	assert.Equal(t, "task-old2", second.DoneToday[0].TaskID)
	assert.Equal(t, "Old task 2", second.DoneToday[0].Title)
}

// TestAPITaskStack_NeedsYou_TitleForTaskOutsideRunWindow verifies that a
// NeedsYou item's title still resolves even when the referenced task's only
// run falls outside the bounded run window buildTaskStack fetches — the
// task ID set passed to ListTasksByIDs must include attention item task IDs,
// not just task IDs discovered from the windowed runs.
func TestAPITaskStack_NeedsYou_TitleForTaskOutsideRunWindow(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	seed := domain.NewRun("seed", "develop")
	seed.ProjectDir = taskStackProjectDir
	seed.State = domain.RunStateSucceeded
	seed.StartedAt = time.Now()
	seed.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, seed))

	old := time.Now().Add(-36 * time.Hour)
	task := &domain.Task{ID: "task-stale", Title: "Stale claim task", Source: domain.TaskSourceExternal, ProjectDir: taskStackProjectDir, CreatedAt: old}
	require.NoError(t, store.SaveTask(ctx, task))

	since := time.Now().Add(-time.Hour)
	h.attentionProvider = &mockAttentionProvider{
		items: map[string][]attention.Item{
			taskStackProjectDir: {
				{Kind: attention.KindStaleClaim, ProjectDir: taskStackProjectDir, TaskID: "task-stale", RunID: "", Reason: "no active run", Since: since, Actions: []string{"retry"}},
			},
		},
	}

	_, stack := getTaskStack(t, h, "")
	require.Len(t, stack.NeedsYou, 1)
	assert.Equal(t, "task-stale", stack.NeedsYou[0].TaskID)
	assert.Equal(t, "Stale claim task", stack.NeedsYou[0].Title)
}
