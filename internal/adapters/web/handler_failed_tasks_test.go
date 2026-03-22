package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedTaskRun creates a run associated with a task ID.
func seedTaskRun(t *testing.T, store *sqlite.Store, id, workflow string, state domain.RunState, taskID string) {
	t.Helper()
	ctx := context.Background()
	run := domain.NewRun(id, workflow)
	run.TaskID = taskID
	if state != domain.RunStatePending {
		run.Start()
	}
	if state == domain.RunStateSucceeded || state == domain.RunStateFailed {
		run.Complete(state)
	}
	if state == domain.RunStateFailed {
		run.ErrorMessage = "something went wrong"
	}
	require.NoError(t, store.CreateRun(ctx, run))
}

func TestFailedTasksDashboard_Empty(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Contains(t, w.Body.String(), "No failed open tasks")
}

func TestFailedTasksDashboard_ShowsFailedTask(t *testing.T) {
	h, store := setupHandler(t)
	seedTaskRun(t, store, "run-f1", "develop", domain.RunStateFailed, "task-A")

	req := httptest.NewRequest("GET", "/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "task-A")
	assert.Contains(t, body, "run-f1")
}

func TestFailedTasksDashboard_HidesSucceededTask(t *testing.T) {
	h, store := setupHandler(t)
	// task-B: first attempt failed, second succeeded — should NOT appear.
	seedTaskRun(t, store, "run-b1", "develop", domain.RunStateFailed, "task-B")
	seedTaskRun(t, store, "run-b2", "develop", domain.RunStateSucceeded, "task-B")

	req := httptest.NewRequest("GET", "/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "task-B")
}

func TestFailedTasksDashboard_HidesTaskWithoutTaskID(t *testing.T) {
	h, store := setupHandler(t)
	// Runs without a task ID should not appear on the failed tasks dashboard.
	seedRun(t, store, "run-no-task", "develop", domain.RunStateFailed)

	req := httptest.NewRequest("GET", "/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotContains(t, w.Body.String(), "run-no-task")
}

func TestAPIFailedTasks_Empty(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/api/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var tasks []failedOpenTaskEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&tasks))
	assert.Empty(t, tasks)
}

func TestAPIFailedTasks_ShowsFailedTask(t *testing.T) {
	h, store := setupHandler(t)
	seedTaskRun(t, store, "run-api-1", "develop", domain.RunStateFailed, "task-X")

	req := httptest.NewRequest("GET", "/api/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var tasks []failedOpenTaskEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&tasks))
	require.Len(t, tasks, 1)
	assert.Equal(t, "task-X", tasks[0].TaskID)
	assert.Equal(t, "run-api-1", tasks[0].LatestRunID)
	assert.Equal(t, 1, tasks[0].FailedCount)
}

func TestAPIFailedTasks_MultipleFailedAttempts(t *testing.T) {
	h, store := setupHandler(t)
	// task-Y: two failed attempts.
	seedTaskRun(t, store, "run-y1", "develop", domain.RunStateFailed, "task-Y")
	seedTaskRun(t, store, "run-y2", "develop", domain.RunStateFailed, "task-Y")

	req := httptest.NewRequest("GET", "/api/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var tasks []failedOpenTaskEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&tasks))
	require.Len(t, tasks, 1)
	assert.Equal(t, "task-Y", tasks[0].TaskID)
	assert.Equal(t, 2, tasks[0].FailedCount)
}

func TestAPIFailedTasks_SucceededNotShown(t *testing.T) {
	h, store := setupHandler(t)
	// task-Z: failed then succeeded — should not appear.
	seedTaskRun(t, store, "run-z1", "develop", domain.RunStateFailed, "task-Z")
	seedTaskRun(t, store, "run-z2", "develop", domain.RunStateSucceeded, "task-Z")

	// task-W: only failed — should appear.
	seedTaskRun(t, store, "run-w1", "develop", domain.RunStateFailed, "task-W")

	req := httptest.NewRequest("GET", "/api/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var tasks []failedOpenTaskEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&tasks))
	require.Len(t, tasks, 1)
	assert.Equal(t, "task-W", tasks[0].TaskID)
}

func TestAPIFailedTasks_BeadProviderFlags(t *testing.T) {
	h, store := setupHandler(t)
	projectDir := "/home/user/projects/beadtest"
	ctx := context.Background()

	// Seed runs with the project dir so that the task provider lookup works.
	run1 := domain.NewRun("run-bead-1", "develop")
	run1.TaskID = "task-inbead"
	run1.ProjectDir = projectDir
	run1.Start()
	run1.Complete(domain.RunStateFailed)
	run1.ErrorMessage = "failed"
	require.NoError(t, store.CreateRun(ctx, run1))

	run2 := domain.NewRun("run-bead-2", "develop")
	run2.TaskID = "task-notinbead"
	run2.ProjectDir = projectDir
	run2.Start()
	run2.Complete(domain.RunStateFailed)
	run2.ErrorMessage = "failed"
	require.NoError(t, store.CreateRun(ctx, run2))

	// Inject a mock task provider that only lists task-inbead as open.
	h.taskProvider = &mockTaskProvider{
		tasks: map[string][]TaskEntry{
			projectDir: {
				{ID: "task-inbead", Status: "open", Title: "Open bead ticket"},
			},
		},
	}

	req := httptest.NewRequest("GET", "/api/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var tasks []failedOpenTaskEntry
	require.NoError(t, json.NewDecoder(w.Body).Decode(&tasks))
	require.Len(t, tasks, 2)

	byID := map[string]failedOpenTaskEntry{}
	for _, te := range tasks {
		byID[te.TaskID] = te
	}
	assert.True(t, byID["task-inbead"].OpenInBead)
	assert.False(t, byID["task-notinbead"].OpenInBead)
}
