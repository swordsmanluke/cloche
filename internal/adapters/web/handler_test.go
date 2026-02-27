package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupHandler(t *testing.T) (*Handler, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	h, err := NewHandler(store, store)
	require.NoError(t, err)
	return h, store
}

func seedRun(t *testing.T, store *sqlite.Store, id, workflow string, state domain.RunState) {
	t.Helper()
	ctx := context.Background()
	run := domain.NewRun(id, workflow)
	if state != domain.RunStatePending {
		run.Start()
		run.ContainerID = "abc123def456789"
	}
	if state == domain.RunStateSucceeded || state == domain.RunStateFailed {
		run.Complete(state)
	}
	if state == domain.RunStateFailed {
		run.ErrorMessage = "something went wrong"
	}
	require.NoError(t, store.CreateRun(ctx, run))
}

func TestRunsList_WithRuns(t *testing.T) {
	h, store := setupHandler(t)
	seedRun(t, store, "test-run-1", "develop", domain.RunStateRunning)
	seedRun(t, store, "test-run-2", "deploy", domain.RunStateSucceeded)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	body := w.Body.String()
	assert.Contains(t, body, "test-run-1")
	assert.Contains(t, body, "test-run-2")
	assert.Contains(t, body, "badge-running")
	assert.Contains(t, body, "badge-succeeded")
	assert.Contains(t, body, "develop")
	assert.Contains(t, body, "deploy")
}

func TestRunsList_Empty(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No runs yet.")
}

func TestRunDetail_WithCaptures(t *testing.T) {
	h, store := setupHandler(t)
	seedRun(t, store, "run-detail-1", "develop", domain.RunStateRunning)

	ctx := context.Background()
	require.NoError(t, store.SaveCapture(ctx, "run-detail-1", &domain.StepExecution{
		StepName:      "implement",
		Result:        "success",
		StartedAt:     time.Now().Add(-5 * time.Minute),
		CompletedAt:   time.Now(),
		PromptText:    "Write hello world",
		AgentOutput:   "Here is the code",
		AttemptNumber: 1,
	}))

	req := httptest.NewRequest("GET", "/runs/run-detail-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "run-detail-1")
	assert.Contains(t, body, "implement")
	assert.Contains(t, body, "Write hello world")
	assert.Contains(t, body, "Here is the code")
}

func TestRunDetail_NotFound(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/runs/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRuns(t *testing.T) {
	h, store := setupHandler(t)
	seedRun(t, store, "api-run-1", "develop", domain.RunStateRunning)
	seedRun(t, store, "api-run-2", "deploy", domain.RunStateSucceeded)

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var runs []apiRun
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &runs))
	assert.Len(t, runs, 2)

	ids := map[string]bool{}
	for _, r := range runs {
		ids[r.ID] = true
	}
	assert.True(t, ids["api-run-1"])
	assert.True(t, ids["api-run-2"])
}

func TestAPIRunDetail(t *testing.T) {
	h, store := setupHandler(t)
	seedRun(t, store, "api-detail-1", "develop", domain.RunStateRunning)

	ctx := context.Background()
	require.NoError(t, store.SaveCapture(ctx, "api-detail-1", &domain.StepExecution{
		StepName:      "build",
		Result:        "success",
		StartedAt:     time.Now().Add(-10 * time.Second),
		CompletedAt:   time.Now(),
		PromptText:    "Build the project",
		AgentOutput:   "Build succeeded",
		AttemptNumber: 1,
	}))

	req := httptest.NewRequest("GET", "/api/runs/api-detail-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var detail apiRunDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Equal(t, "api-detail-1", detail.ID)
	assert.Equal(t, "running", detail.State)
	assert.Len(t, detail.Steps, 1)
	assert.Equal(t, "build", detail.Steps[0].StepName)
	assert.Equal(t, "Build the project", detail.Steps[0].PromptText)
	assert.Equal(t, "Build succeeded", detail.Steps[0].AgentOutput)
	assert.NotEmpty(t, detail.Steps[0].Duration)
}

func TestStaticCSS(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/static/style.css", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), ":root")
	assert.Contains(t, w.Body.String(), "badge-running")
}

func TestHelpers(t *testing.T) {
	t.Run("stateColor", func(t *testing.T) {
		assert.Equal(t, "pending", stateColor(domain.RunStatePending))
		assert.Equal(t, "running", stateColor(domain.RunStateRunning))
		assert.Equal(t, "succeeded", stateColor(domain.RunStateSucceeded))
		assert.Equal(t, "failed", stateColor(domain.RunStateFailed))
		assert.Equal(t, "cancelled", stateColor(domain.RunStateCancelled))
	})

	t.Run("formatTime", func(t *testing.T) {
		assert.Equal(t, "", formatTime(time.Time{}))
		ts := time.Date(2026, 2, 26, 14, 30, 0, 0, time.UTC)
		assert.Equal(t, "2026-02-26 14:30:00", formatTime(ts))
	})

	t.Run("formatDuration", func(t *testing.T) {
		now := time.Now()
		assert.Equal(t, "", formatDuration(time.Time{}, now))
		assert.Equal(t, "", formatDuration(now, time.Time{}))
		assert.Equal(t, "500ms", formatDuration(now, now.Add(500*time.Millisecond)))
		assert.Equal(t, "5.0s", formatDuration(now, now.Add(5*time.Second)))
		assert.Equal(t, "2m30s", formatDuration(now, now.Add(2*time.Minute+30*time.Second)))
	})

	t.Run("truncate", func(t *testing.T) {
		assert.Equal(t, "hello", truncate("hello", 10))
		assert.Equal(t, "hel...", truncate("hello world", 3))
	})

	t.Run("shortContainerID", func(t *testing.T) {
		assert.Equal(t, "abc123def456", shortContainerID("abc123def456789abcdef"))
		assert.Equal(t, "short", shortContainerID("short"))
	})
}
