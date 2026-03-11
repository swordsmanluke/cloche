package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockContainerManager implements ContainerManager for testing.
type mockContainerManager struct {
	containers map[string]bool // containerID -> exists
	running    map[string]bool // containerID -> running
	removed    []string
	stopped    []string
}

func newMockContainerManager() *mockContainerManager {
	return &mockContainerManager{
		containers: map[string]bool{},
		running:    map[string]bool{},
	}
}

func (m *mockContainerManager) Logs(_ context.Context, containerID string) (string, error) {
	return "mock logs", nil
}

func (m *mockContainerManager) Stop(_ context.Context, containerID string) error {
	if !m.containers[containerID] {
		return fmt.Errorf("container %s not found", containerID)
	}
	m.running[containerID] = false
	m.stopped = append(m.stopped, containerID)
	return nil
}

func (m *mockContainerManager) Remove(_ context.Context, containerID string) error {
	if !m.containers[containerID] {
		return fmt.Errorf("container %s not found", containerID)
	}
	delete(m.containers, containerID)
	m.removed = append(m.removed, containerID)
	return nil
}

func (m *mockContainerManager) Inspect(_ context.Context, containerID string) (*ports.ContainerStatus, error) {
	if !m.containers[containerID] {
		return nil, fmt.Errorf("container %s not found", containerID)
	}
	return &ports.ContainerStatus{Running: m.running[containerID], ExitCode: 0}, nil
}

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
	seedRunWithProject(t, store, id, workflow, state, "")
}

func seedRunWithProject(t *testing.T, store *sqlite.Store, id, workflow string, state domain.RunState, projectDir string) {
	t.Helper()
	ctx := context.Background()
	run := domain.NewRun(id, workflow)
	run.ProjectDir = projectDir
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

	req := httptest.NewRequest("GET", "/runs", nil)
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

	req := httptest.NewRequest("GET", "/runs", nil)
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
		StepName:    "implement",
		Result:      "success",
		StartedAt:   time.Now().Add(-5 * time.Minute),
		CompletedAt: time.Now(),
	}))

	req := httptest.NewRequest("GET", "/runs/run-detail-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "run-detail-1")
	assert.Contains(t, body, "implement")
}

func TestRunDetail_AccordionStatePreserved(t *testing.T) {
	// Verify the rendered page includes JavaScript that preserves accordion state
	// during SSE polling updates (captures open/closed state before DOM rebuild).
	h, store := setupHandler(t)
	seedRun(t, store, "run-accordion-1", "develop", domain.RunStateRunning)

	ctx := context.Background()
	require.NoError(t, store.SaveCapture(ctx, "run-accordion-1", &domain.StepExecution{
		StepName:    "build",
		Result:      "success",
		StartedAt:   time.Now().Add(-5 * time.Minute),
		CompletedAt: time.Now(),
	}))

	req := httptest.NewRequest("GET", "/runs/run-accordion-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The poll function should capture accordion state before rebuilding the DOM
	assert.Contains(t, body, "openAccordions")
	assert.Contains(t, body, "loadedOutputs")
	// Verify it restores loaded output content after DOM rebuild
	assert.Contains(t, body, "loadedOutputs[key]")
}

func TestRunDetail_LogScrollStabilization(t *testing.T) {
	// Verify the rendered page stabilizes scroll position when SSE log events
	// arrive: saves scrollTop before append, auto-scrolls only when at bottom,
	// and restores position otherwise.
	h, store := setupHandler(t)
	seedRun(t, store, "run-scroll-1", "develop", domain.RunStateRunning)

	req := httptest.NewRequest("GET", "/runs/run-scroll-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Must capture scroll position before DOM mutation
	assert.Contains(t, body, "savedScrollTop")
	// Must check if at bottom before appending content
	assert.Contains(t, body, "var atBottom = logViewer.scrollHeight - savedScrollTop - logViewer.clientHeight < 40")
	// Must restore scroll position when not at bottom
	assert.Contains(t, body, "logViewer.scrollTop = savedScrollTop")
	// Must NOT use a detached autoScroll state variable (race-prone)
	assert.NotContains(t, body, "var autoScroll")
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
		StepName:    "build",
		Result:      "success",
		StartedAt:   time.Now().Add(-10 * time.Second),
		CompletedAt: time.Now(),
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

	t.Run("formatSmartDuration", func(t *testing.T) {
		assert.Equal(t, "0s", formatSmartDuration(0))
		assert.Equal(t, "30s", formatSmartDuration(30*time.Second))
		assert.Equal(t, "5m", formatSmartDuration(5*time.Minute))
		assert.Equal(t, "1h", formatSmartDuration(1*time.Hour))
		assert.Equal(t, "1h20m", formatSmartDuration(1*time.Hour+20*time.Minute))
		assert.Equal(t, "3h", formatSmartDuration(3*time.Hour))
		assert.Equal(t, "2h5m", formatSmartDuration(2*time.Hour+5*time.Minute+10*time.Second))
	})

	t.Run("roundRelativeTime", func(t *testing.T) {
		assert.Equal(t, "<1m ago", roundRelativeTime(30*time.Second))
		assert.Equal(t, "1m ago", roundRelativeTime(1*time.Minute))
		assert.Equal(t, "1m ago", roundRelativeTime(2*time.Minute))
		assert.Equal(t, "5m ago", roundRelativeTime(5*time.Minute))
		assert.Equal(t, "5m ago", roundRelativeTime(7*time.Minute))
		assert.Equal(t, "10m ago", roundRelativeTime(10*time.Minute))
		assert.Equal(t, "10m ago", roundRelativeTime(12*time.Minute))
		assert.Equal(t, "15m ago", roundRelativeTime(15*time.Minute))
		assert.Equal(t, "15m ago", roundRelativeTime(19*time.Minute))
		assert.Equal(t, "30m ago", roundRelativeTime(25*time.Minute))
		assert.Equal(t, "30m ago", roundRelativeTime(37*time.Minute))
		assert.Equal(t, "45m ago", roundRelativeTime(40*time.Minute))
		assert.Equal(t, "45m ago", roundRelativeTime(52*time.Minute))
		assert.Equal(t, "1 hour ago", roundRelativeTime(55*time.Minute))
		assert.Equal(t, "1 hour ago", roundRelativeTime(1*time.Hour))
		assert.Equal(t, "2 hours ago", roundRelativeTime(2*time.Hour))
		assert.Equal(t, "5 hours ago", roundRelativeTime(5*time.Hour+30*time.Minute))
		assert.Equal(t, "1 day ago", roundRelativeTime(24*time.Hour))
		assert.Equal(t, "3 days ago", roundRelativeTime(72*time.Hour))
	})

	t.Run("formatRunTimingAt", func(t *testing.T) {
		now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
		started := now.Add(-20 * time.Minute)
		completed := now.Add(-10 * time.Minute)

		// Pending: no start time
		assert.Equal(t, "", formatRunTimingAt(domain.RunStatePending, time.Time{}, time.Time{}, now))

		// Running: shows elapsed duration
		assert.Equal(t, "20m", formatRunTimingAt(domain.RunStateRunning, started, time.Time{}, now))

		// Running: short duration
		assert.Equal(t, "45s", formatRunTimingAt(domain.RunStateRunning, now.Add(-45*time.Second), time.Time{}, now))

		// Completed: "duration, X ago"
		assert.Equal(t, "10m, 10m ago", formatRunTimingAt(domain.RunStateSucceeded, started, completed, now))

		// Failed: "duration, X ago"
		assert.Equal(t, "10m, 10m ago", formatRunTimingAt(domain.RunStateFailed, started, completed, now))

		// Cancelled: "duration, X ago"
		longAgo := now.Add(-3 * time.Hour)
		assert.Equal(t, "2h50m, 10m ago", formatRunTimingAt(domain.RunStateCancelled, longAgo, completed, now))

		// Completed but no completedAt: empty
		assert.Equal(t, "", formatRunTimingAt(domain.RunStateSucceeded, started, time.Time{}, now))
	})

	t.Run("projectLabels", func(t *testing.T) {
		// No conflict: show base name only
		labels := projectLabels([]string{"/home/user/alpha", "/home/user/beta"})
		assert.Equal(t, "alpha", labels["/home/user/alpha"])
		assert.Equal(t, "beta", labels["/home/user/beta"])

		// Conflict: two dirs share the same base name
		labels = projectLabels([]string{"/home/foo/bar", "/home/baz/bar"})
		assert.Equal(t, "foo/bar", labels["/home/foo/bar"])
		assert.Equal(t, "baz/bar", labels["/home/baz/bar"])

		// Mixed: some conflict, some don't
		labels = projectLabels([]string{"/a/bar", "/b/bar", "/c/unique"})
		assert.Equal(t, "a/bar", labels["/a/bar"])
		assert.Equal(t, "b/bar", labels["/b/bar"])
		assert.Equal(t, "unique", labels["/c/unique"])

		// Empty list
		labels = projectLabels(nil)
		assert.Empty(t, labels)
	})
}

func TestRunsList_ProjectFilter(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "run-a1", "develop", domain.RunStateRunning, "/home/user/alpha")
	seedRunWithProject(t, store, "run-a2", "develop", domain.RunStateSucceeded, "/home/user/alpha")
	seedRunWithProject(t, store, "run-b1", "deploy", domain.RunStateRunning, "/home/user/beta")

	// Without filter: all runs shown
	req := httptest.NewRequest("GET", "/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "run-a1")
	assert.Contains(t, body, "run-b1")

	// With project filter: only matching runs
	req = httptest.NewRequest("GET", "/runs?project=/home/user/alpha", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	body = w.Body.String()
	assert.Contains(t, body, "run-a1")
	assert.Contains(t, body, "run-a2")
	assert.NotContains(t, body, "run-b1")
}

func TestRunsList_ProjectColumn(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "run-p1", "develop", domain.RunStateRunning, "/home/user/myproject")

	req := httptest.NewRequest("GET", "/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "myproject")
	assert.Contains(t, body, "Project")
}

func TestAPIRuns_ProjectFilter(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "api-a1", "develop", domain.RunStateRunning, "/home/user/alpha")
	seedRunWithProject(t, store, "api-b1", "deploy", domain.RunStateRunning, "/home/user/beta")

	// Without filter: all runs
	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var allRuns []apiRun
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &allRuns))
	assert.Len(t, allRuns, 2)

	// With filter: only matching
	req = httptest.NewRequest("GET", "/api/runs?project=/home/user/alpha", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var filtered []apiRun
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &filtered))
	assert.Len(t, filtered, 1)
	assert.Equal(t, "api-a1", filtered[0].ID)
	assert.Equal(t, "/home/user/alpha", filtered[0].ProjectDir)
	assert.Equal(t, "alpha", filtered[0].ProjectLabel)
}

func setupHandlerWithContainerManager(t *testing.T) (*Handler, *sqlite.Store, *mockContainerManager) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	mgr := newMockContainerManager()
	h, err := NewHandler(store, store, WithContainerManager(mgr))
	require.NoError(t, err)
	return h, store, mgr
}

func seedRunWithContainer(t *testing.T, store *sqlite.Store, mgr *mockContainerManager, id, workflow, projectDir, containerID string, kept bool) {
	t.Helper()
	ctx := context.Background()
	run := domain.NewRun(id, workflow)
	run.ProjectDir = projectDir
	run.Start()
	run.ContainerID = containerID
	run.ContainerKept = kept
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))
	if kept {
		mgr.containers[containerID] = true
	}
}

func TestRunDetail_ContainerRunning(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)

	ctx := context.Background()
	run := domain.NewRun("run-cr", "develop")
	run.ProjectDir = "/proj"
	run.Start()
	run.ContainerID = "cid-running"
	require.NoError(t, store.CreateRun(ctx, run))
	mgr.containers["cid-running"] = true
	mgr.running["cid-running"] = true

	req := httptest.NewRequest("GET", "/runs/run-cr", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Container running")
	assert.Contains(t, body, `badge-running">Container running`)
}

func TestRunDetail_ContainerStopped(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)

	ctx := context.Background()
	run := domain.NewRun("run-cs", "develop")
	run.ProjectDir = "/proj"
	run.Start()
	run.ContainerID = "cid-stopped"
	run.ContainerKept = false
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))
	mgr.containers["cid-stopped"] = true // container exists but not running, not kept

	req := httptest.NewRequest("GET", "/runs/run-cs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Container stopped")
	assert.Contains(t, body, `badge-stopped">Container stopped`)
}

func TestRunDetail_ContainerStoppedTerminal_ShowsDeleteButton(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)

	for _, state := range []domain.RunState{domain.RunStateSucceeded, domain.RunStateFailed, domain.RunStateCancelled} {
		t.Run(string(state), func(t *testing.T) {
			id := "run-cst-" + string(state)
			ctx := context.Background()
			run := domain.NewRun(id, "develop")
			run.ProjectDir = "/proj"
			run.Start()
			run.ContainerID = "cid-stopped-" + string(state)
			run.ContainerKept = false
			run.Complete(state)
			require.NoError(t, store.CreateRun(ctx, run))
			mgr.containers[run.ContainerID] = true // container exists but not running, not kept

			req := httptest.NewRequest("GET", "/runs/"+id, nil)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			body := w.Body.String()
			assert.Contains(t, body, "Container stopped")
			assert.Contains(t, body, `id="delete-container-btn"`, "should show delete button for terminal run with stopped container")
		})
	}
}

func TestRunDetail_ContainerStoppedRunning_NoDeleteButton(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)

	ctx := context.Background()
	run := domain.NewRun("run-csr", "develop")
	run.ProjectDir = "/proj"
	run.Start()
	run.ContainerID = "cid-stopped-running"
	run.ContainerKept = false
	require.NoError(t, store.CreateRun(ctx, run))
	mgr.containers["cid-stopped-running"] = true
	// container not running, but run state is still "running" (not terminal)
	// containerState will return "stopped" since ContainerKept=false

	req := httptest.NewRequest("GET", "/runs/run-csr", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Container stopped")
	// The HTML container-status section should NOT have the delete button.
	// We check the dd#container-status block specifically, not the full body
	// (which includes JS that references the button ID as string literals).
	statusStart := "container-status"
	statusEnd := "</dd>"
	idx := strings.Index(body, statusStart)
	require.NotEqual(t, -1, idx, "container-status element must exist")
	endIdx := strings.Index(body[idx:], statusEnd)
	require.NotEqual(t, -1, endIdx, "closing </dd> must exist")
	statusSection := body[idx : idx+endIdx+len(statusEnd)]
	assert.NotContains(t, statusSection, "Delete Container", "should NOT show delete button for non-terminal run")
}

func TestRunDetail_ContainerAvailable(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-c1", "develop", "/proj", "cid-1234567890ab", true)

	req := httptest.NewRequest("GET", "/runs/run-c1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Container available")
	assert.Contains(t, body, `badge-succeeded">Container available`)
	assert.Contains(t, body, `id="delete-container-btn"`)
}

func TestRunDetail_ContainerRemoved(t *testing.T) {
	h, store, _ := setupHandlerWithContainerManager(t)

	ctx := context.Background()
	run := domain.NewRun("run-c2", "develop")
	run.ProjectDir = "/proj"
	run.Start()
	run.ContainerID = "cid-gone"
	run.ContainerKept = false
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))
	// container NOT in mock's containers map → Inspect will fail → "removed"

	req := httptest.NewRequest("GET", "/runs/run-c2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Container removed")
	assert.Contains(t, body, `badge-pending">Container removed`)
}

func TestAPIDeleteContainer_Success(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-del1", "develop", "/proj", "cid-to-delete", true)

	req := httptest.NewRequest("DELETE", "/api/runs/run-del1/container", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp["status"])

	// Verify container was removed
	assert.Contains(t, mgr.removed, "cid-to-delete")

	// Verify run was updated
	run, err := store.GetRun(context.Background(), "run-del1")
	require.NoError(t, err)
	assert.False(t, run.ContainerKept)
}

func TestAPIDeleteContainer_StoppedContainer(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)

	ctx := context.Background()
	run := domain.NewRun("run-del-stopped", "develop")
	run.ProjectDir = "/proj"
	run.Start()
	run.ContainerID = "cid-stopped-del"
	run.ContainerKept = false
	run.Complete(domain.RunStateFailed)
	require.NoError(t, store.CreateRun(ctx, run))
	mgr.containers["cid-stopped-del"] = true

	req := httptest.NewRequest("DELETE", "/api/runs/run-del-stopped/container", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp["status"])
	assert.Contains(t, mgr.removed, "cid-stopped-del")
}

func TestAPIDeleteContainer_RunNotFound(t *testing.T) {
	h, _, _ := setupHandlerWithContainerManager(t)

	req := httptest.NewRequest("DELETE", "/api/runs/nonexistent/container", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIDeleteContainer_NoManager(t *testing.T) {
	h, store := setupHandler(t) // no container manager
	seedRun(t, store, "run-noman", "develop", domain.RunStateSucceeded)

	req := httptest.NewRequest("DELETE", "/api/runs/run-noman/container", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIRunDetail_ContainerState(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)

	// Seed a running container
	ctx := context.Background()
	run := domain.NewRun("api-cs1", "develop")
	run.ProjectDir = "/proj"
	run.Start()
	run.ContainerID = "cid-api-running"
	require.NoError(t, store.CreateRun(ctx, run))
	mgr.containers["cid-api-running"] = true
	mgr.running["cid-api-running"] = true

	req := httptest.NewRequest("GET", "/api/runs/api-cs1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var detail apiRunDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Equal(t, "running", detail.ContainerState)

	// Now stop it
	mgr.running["cid-api-running"] = false

	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/runs/api-cs1", nil))

	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Equal(t, "stopped", detail.ContainerState)
}

func TestRunsList_ContainerCount(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-cc1", "develop", "/home/user/alpha", "cid-1", true)
	seedRunWithContainer(t, store, mgr, "run-cc2", "develop", "/home/user/alpha", "cid-2", true)
	seedRunWithContainer(t, store, mgr, "run-cc3", "deploy", "/home/user/beta", "cid-3", false)

	req := httptest.NewRequest("GET", "/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// alpha has 2 retained containers
	assert.Contains(t, body, "2 containers")
	// beta has 0, so no count should appear
	assert.NotContains(t, body, "0 container")
}

func TestAPIProjects(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "p1", "develop", domain.RunStateRunning, "/home/user/alpha")
	seedRunWithProject(t, store, "p2", "develop", domain.RunStateRunning, "/home/user/beta")
	seedRunWithProject(t, store, "p3", "develop", domain.RunStateSucceeded, "/home/user/alpha")

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	type apiHealth struct {
		Status string `json:"status"`
		Passed int    `json:"passed"`
		Failed int    `json:"failed"`
		Total  int    `json:"total"`
	}
	type project struct {
		Dir         string    `json:"dir"`
		Label       string    `json:"label"`
		Health      apiHealth `json:"health"`
		ActiveCount int       `json:"active_count"`
	}
	var projects []project
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &projects))
	assert.Len(t, projects, 2)

	byDir := map[string]project{}
	for _, p := range projects {
		byDir[p.Dir] = p
	}
	assert.Equal(t, "alpha", byDir["/home/user/alpha"].Label)
	assert.Equal(t, "beta", byDir["/home/user/beta"].Label)
	// alpha has 1 running + 1 succeeded = yellow health (mix), 1 active
	assert.Equal(t, 1, byDir["/home/user/alpha"].ActiveCount)
	assert.NotEmpty(t, byDir["/home/user/alpha"].Health.Status)
	// beta has 1 running = blue (all in-progress), 1 active
	assert.Equal(t, "blue", byDir["/home/user/beta"].Health.Status)
	assert.Equal(t, 1, byDir["/home/user/beta"].ActiveCount)
}

func TestProjectOverview_WithProjects(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "ov-1", "develop", domain.RunStateSucceeded, "/home/user/alpha")
	seedRunWithProject(t, store, "ov-2", "develop", domain.RunStateFailed, "/home/user/alpha")
	seedRunWithProject(t, store, "ov-3", "develop", domain.RunStateRunning, "/home/user/beta")

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	body := w.Body.String()
	// Project cards should be present
	assert.Contains(t, body, "project-card")
	assert.Contains(t, body, "alpha")
	assert.Contains(t, body, "beta")
	// Health dots
	assert.Contains(t, body, "health-dot")
	// Run history dots
	assert.Contains(t, body, "run-dot")
	// Quick actions
	assert.Contains(t, body, "View Runs")
}

func TestProjectOverview_Empty(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "No projects yet")
}

func TestHealthColor(t *testing.T) {
	assert.Equal(t, "green", healthColor(domain.HealthGreen))
	assert.Equal(t, "yellow", healthColor(domain.HealthYellow))
	assert.Equal(t, "red", healthColor(domain.HealthRed))
	assert.Equal(t, "blue", healthColor(domain.HealthBlue))
	assert.Equal(t, "grey", healthColor(domain.HealthGrey))
	assert.Equal(t, "grey", healthColor("unknown"))
}

func TestAPIProjects_HealthData(t *testing.T) {
	h, store := setupHandler(t)
	// alpha: 3 succeeded, 1 failed → yellow
	seedRunWithProject(t, store, "h1", "develop", domain.RunStateSucceeded, "/home/user/alpha")
	seedRunWithProject(t, store, "h2", "develop", domain.RunStateSucceeded, "/home/user/alpha")
	seedRunWithProject(t, store, "h3", "develop", domain.RunStateFailed, "/home/user/alpha")
	seedRunWithProject(t, store, "h4", "develop", domain.RunStateSucceeded, "/home/user/alpha")

	// beta: all succeeded → green
	seedRunWithProject(t, store, "h5", "develop", domain.RunStateSucceeded, "/home/user/beta")
	seedRunWithProject(t, store, "h6", "develop", domain.RunStateSucceeded, "/home/user/beta")

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	type health struct {
		Status string `json:"status"`
		Passed int    `json:"passed"`
		Failed int    `json:"failed"`
		Total  int    `json:"total"`
	}
	type project struct {
		Dir    string `json:"dir"`
		Label  string `json:"label"`
		Health health `json:"health"`
	}
	var projects []project
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &projects))
	assert.Len(t, projects, 2)

	byDir := map[string]project{}
	for _, p := range projects {
		byDir[p.Dir] = p
	}

	alpha := byDir["/home/user/alpha"]
	assert.Equal(t, "yellow", alpha.Health.Status)
	assert.Equal(t, 3, alpha.Health.Passed)
	assert.Equal(t, 1, alpha.Health.Failed)
	assert.Equal(t, 4, alpha.Health.Total)

	beta := byDir["/home/user/beta"]
	assert.Equal(t, "green", beta.Health.Status)
	assert.Equal(t, 2, beta.Health.Passed)
	assert.Equal(t, 0, beta.Health.Failed)
	assert.Equal(t, 2, beta.Health.Total)
}

func TestResolveFileRef(t *testing.T) {
	// Create a temp directory with a test file
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "prompts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "prompts", "implement.md"), []byte("# Implement\nDo the thing."), 0o644))

	t.Run("file() reference resolves to contents", func(t *testing.T) {
		content, err := resolveFileRef(`file("prompts/implement.md")`, dir)
		require.NoError(t, err)
		assert.Equal(t, "# Implement\nDo the thing.", content)
	})

	t.Run("file() reference with missing file returns error", func(t *testing.T) {
		_, err := resolveFileRef(`file("prompts/nonexistent.md")`, dir)
		assert.Error(t, err)
	})

	t.Run("plain string returned as-is", func(t *testing.T) {
		content, err := resolveFileRef("echo hello", dir)
		require.NoError(t, err)
		assert.Equal(t, "echo hello", content)
	})

	t.Run("quoted string returned as-is", func(t *testing.T) {
		content, err := resolveFileRef(`"some value"`, dir)
		require.NoError(t, err)
		assert.Equal(t, `"some value"`, content)
	})
}

func TestAPIStepContent_FileRef(t *testing.T) {
	h, store := setupHandler(t)

	// Create a temp project directory with a workflow and prompt file
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "prompts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "prompts", "implement.md"), []byte("Build the feature."), 0o644))

	workflowContent := `workflow "develop" {
    step implement {
        prompt = file(".cloche/prompts/implement.md")
        results = [success, fail]
    }
    implement:success -> done
    implement:fail -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"), []byte(workflowContent), 0o644))

	// Seed a run so the project is known
	seedRunWithProject(t, store, "sc-1", "develop", domain.RunStateRunning, dir)

	// Request step content
	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows/develop/steps/implement/content", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "Build the feature.", w.Body.String())
}

func TestAPIWorkflows_IncludesHostWorkflows(t *testing.T) {
	h, store := setupHandler(t)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755))

	// Container workflow
	containerWf := `workflow "develop" {
    step implement {
        prompt = "Build it"
        results = [success, fail]
    }
    implement:success -> done
    implement:fail -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"), []byte(containerWf), 0o644))

	// Host workflow
	hostWf := `workflow "main" {
    step prepare {
        run = "echo prepare"
        results = [ready]
    }
    step build {
        workflow_name = "develop"
        results = [success, fail]
    }
    prepare:ready -> build
    build:success -> done
    build:fail -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "host.cloche"), []byte(hostWf), 0o644))

	seedRunWithProject(t, store, "hw-1", "develop", domain.RunStateRunning, dir)

	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var workflows []struct {
		Name     string `json:"name"`
		Location string `json:"location"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &workflows))

	// Should have both container and host workflows
	assert.Len(t, workflows, 2)

	var locations = map[string]string{}
	for _, wf := range workflows {
		locations[wf.Name] = wf.Location
	}
	assert.Equal(t, "container", locations["develop"])
	assert.Equal(t, "host", locations["main"])
}

func TestAPIStepContent_HostWorkflow(t *testing.T) {
	h, store := setupHandler(t)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755))

	hostWf := `workflow "main" {
    step prepare {
        run = "echo hello"
        results = [ready]
    }
    step build {
        workflow_name = "develop"
        results = [success, fail]
    }
    prepare:ready -> build
    build:success -> done
    build:fail -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "host.cloche"), []byte(hostWf), 0o644))

	seedRunWithProject(t, store, "hw-2", "main", domain.RunStateRunning, dir)

	// Test script step content
	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows/main/steps/prepare/content", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "echo hello", w.Body.String())

	// Test workflow_name step content
	req2 := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows/main/steps/build/content", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)

	assert.Equal(t, http.StatusOK, w2.Code)
	assert.Equal(t, "Dispatches workflow: develop", w2.Body.String())
}

func TestAPIStepContent_ScriptFileFromCommand(t *testing.T) {
	h, store := setupHandler(t)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche", "scripts"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "scripts", "setup.sh"), []byte("#!/bin/bash\necho setup"), 0o644))

	hostWf := `workflow "main" {
    step setup {
        run = "bash .cloche/scripts/setup.sh"
        results = [success, fail]
    }
    setup:success -> done
    setup:fail -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "host.cloche"), []byte(hostWf), 0o644))

	seedRunWithProject(t, store, "sf-1", "main", domain.RunStateRunning, dir)

	// Script step should return the script file contents, not the command
	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows/main/steps/setup/content", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "#!/bin/bash\necho setup", w.Body.String())
}

func TestAPIStepContent_InlineCommand(t *testing.T) {
	h, store := setupHandler(t)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755))

	hostWf := `workflow "main" {
    step test {
        run = "go test ./... 2>&1"
        results = [success, fail]
    }
    test:success -> done
    test:fail -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "host.cloche"), []byte(hostWf), 0o644))

	seedRunWithProject(t, store, "ic-1", "main", domain.RunStateRunning, dir)

	// Inline command with no script file should return the command itself
	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows/main/steps/test/content", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "go test ./... 2>&1", w.Body.String())
}

func TestAPIStopRun_Success(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)

	ctx := context.Background()
	run := domain.NewRun("run-stop-1", "develop")
	run.ProjectDir = "/proj"
	run.Start()
	run.ContainerID = "cid-to-stop"
	require.NoError(t, store.CreateRun(ctx, run))
	mgr.containers["cid-to-stop"] = true
	mgr.running["cid-to-stop"] = true

	req := httptest.NewRequest("POST", "/api/runs/run-stop-1/stop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp["status"])

	// Verify container was stopped
	assert.Contains(t, mgr.stopped, "cid-to-stop")

	// Verify run state is cancelled
	updated, err := store.GetRun(ctx, "run-stop-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateCancelled, updated.State)
}

func TestAPIStopRun_NotActive(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-stop-done", "develop", "/proj", "cid-done", false)

	req := httptest.NewRequest("POST", "/api/runs/run-stop-done/stop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "not active")
}

func TestAPIStopRun_NotFound(t *testing.T) {
	h, _, _ := setupHandlerWithContainerManager(t)

	req := httptest.NewRequest("POST", "/api/runs/nonexistent/stop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIStopRun_NoManager(t *testing.T) {
	h, store := setupHandler(t) // no container manager
	seedRun(t, store, "run-stop-noman", "develop", domain.RunStateRunning)

	req := httptest.NewRequest("POST", "/api/runs/run-stop-noman/stop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRunDetail_CancelButton_ShownForActiveRuns(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)

	ctx := context.Background()
	run := domain.NewRun("run-cancel-btn", "develop")
	run.ProjectDir = "/proj"
	run.Start()
	run.ContainerID = "cid-cancel"
	require.NoError(t, store.CreateRun(ctx, run))
	mgr.containers["cid-cancel"] = true
	mgr.running["cid-cancel"] = true

	req := httptest.NewRequest("GET", "/runs/run-cancel-btn", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `id="cancel-run-btn"`)
	assert.Contains(t, body, "Cancel")
}

func TestRunDetail_CancelButton_HiddenForTerminalRuns(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-no-cancel", "develop", "/proj", "cid-terminal", false)

	req := httptest.NewRequest("GET", "/runs/run-no-cancel", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	// The cancel button ID should not appear in the HTML header section
	// (it may appear in the JS as a string literal, so check the h1 area)
	h1Start := strings.Index(body, "<h1>")
	h1End := strings.Index(body, "</h1>")
	require.NotEqual(t, -1, h1Start)
	require.NotEqual(t, -1, h1End)
	h1Section := body[h1Start : h1End+len("</h1>")]
	assert.NotContains(t, h1Section, `id="cancel-run-btn"`)
}

func TestAPIDeleteProjectContainers_Success(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-pd1", "develop", "/home/user/alpha", "cid-pd1", true)
	seedRunWithContainer(t, store, mgr, "run-pd2", "develop", "/home/user/alpha", "cid-pd2", true)
	seedRunWithContainer(t, store, mgr, "run-pd3", "deploy", "/home/user/beta", "cid-pd3", true)

	req := httptest.NewRequest("DELETE", "/api/projects/alpha/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(2), resp["deleted"])

	// Verify containers were removed
	assert.Contains(t, mgr.removed, "cid-pd1")
	assert.Contains(t, mgr.removed, "cid-pd2")
	assert.NotContains(t, mgr.removed, "cid-pd3")

	// Verify runs updated
	run1, _ := store.GetRun(context.Background(), "run-pd1")
	assert.False(t, run1.ContainerKept)
	run2, _ := store.GetRun(context.Background(), "run-pd2")
	assert.False(t, run2.ContainerKept)
	// beta run unchanged
	run3, _ := store.GetRun(context.Background(), "run-pd3")
	assert.True(t, run3.ContainerKept)
}

func TestAPIDeleteProjectContainers_SkipsRunning(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-pdr1", "develop", "/home/user/alpha", "cid-pdr1", true)
	mgr.running["cid-pdr1"] = true // mark as running

	seedRunWithContainer(t, store, mgr, "run-pdr2", "develop", "/home/user/alpha", "cid-pdr2", true)

	req := httptest.NewRequest("DELETE", "/api/projects/alpha/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(1), resp["deleted"])

	// Running container not removed
	assert.NotContains(t, mgr.removed, "cid-pdr1")
	assert.Contains(t, mgr.removed, "cid-pdr2")
}

func TestAPIDeleteProjectContainers_SkipsNonKept(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-pdk1", "develop", "/home/user/alpha", "cid-pdk1", false) // not kept

	req := httptest.NewRequest("DELETE", "/api/projects/alpha/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(0), resp["deleted"])
}

func TestAPIDeleteProjectContainers_NoManager(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "run-pdnm", "develop", domain.RunStateSucceeded, "/home/user/alpha")

	req := httptest.NewRequest("DELETE", "/api/projects/alpha/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIDeleteProjectContainers_ProjectNotFound(t *testing.T) {
	h, _, _ := setupHandlerWithContainerManager(t)

	req := httptest.NewRequest("DELETE", "/api/projects/nonexistent/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestProjectDetail_ContainerDeleteButton(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-pdb1", "develop", "/home/user/alpha", "cid-pdb1", true)
	seedRunWithContainer(t, store, mgr, "run-pdb2", "develop", "/home/user/alpha", "cid-pdb2", true)

	req := httptest.NewRequest("GET", "/projects/alpha", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `id="delete-containers-btn"`)
	assert.Contains(t, body, "Delete 2 containers")
}

func TestProjectDetail_NoButtonWhenNoContainers(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-pdnb1", "develop", "/home/user/alpha", "cid-pdnb1", false)

	req := httptest.NewRequest("GET", "/projects/alpha", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, `id="delete-containers-btn"`)
}

func TestAPIProjects_HealthNoRuns(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// No projects means empty array
	type project struct {
		Dir   string `json:"dir"`
		Label string `json:"label"`
	}
	var projects []project
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &projects))
	assert.Empty(t, projects)
}

func TestRunDetail_ParentChildLinks(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create parent (host) run
	parent := domain.NewRun("parent-host-1", "main")
	parent.IsHost = true
	parent.ProjectDir = "/project"
	parent.Start()
	require.NoError(t, store.CreateRun(ctx, parent))

	// Create child run with ParentRunID
	child := domain.NewRun("child-run-1", "develop")
	child.ProjectDir = "/project"
	child.ParentRunID = "parent-host-1"
	child.Start()
	child.Title = "Implement feature X"
	require.NoError(t, store.CreateRun(ctx, child))

	// Check child's detail page shows parent link
	req := httptest.NewRequest("GET", "/runs/child-run-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "parent-host-1")
	assert.Contains(t, body, `href="/runs/parent-host-1"`)
	assert.Contains(t, body, "Parent Run")

	// Check parent's detail page shows child runs section
	req = httptest.NewRequest("GET", "/runs/parent-host-1", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body = w.Body.String()
	assert.Contains(t, body, "Child Runs")
	assert.Contains(t, body, "child-run-1")
	assert.Contains(t, body, `href="/runs/child-run-1"`)
	assert.Contains(t, body, "Implement feature X")
}

func TestRunDetail_NoParentChild(t *testing.T) {
	h, store := setupHandler(t)
	seedRun(t, store, "standalone-1", "develop", domain.RunStateRunning)

	req := httptest.NewRequest("GET", "/runs/standalone-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, "Parent Run")
	assert.NotContains(t, body, "Child Runs")
}

func TestAPIRunDetail_ChildRuns(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create parent run
	parent := domain.NewRun("api-parent-1", "main")
	parent.IsHost = true
	parent.ProjectDir = "/project"
	parent.Start()
	require.NoError(t, store.CreateRun(ctx, parent))

	// Create child runs
	child1 := domain.NewRun("api-child-1", "develop")
	child1.ProjectDir = "/project"
	child1.ParentRunID = "api-parent-1"
	child1.Start()
	require.NoError(t, store.CreateRun(ctx, child1))

	child2 := domain.NewRun("api-child-2", "develop")
	child2.ProjectDir = "/project"
	child2.ParentRunID = "api-parent-1"
	child2.Start()
	require.NoError(t, store.CreateRun(ctx, child2))

	// API detail for parent should include child_runs
	req := httptest.NewRequest("GET", "/api/runs/api-parent-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var detail apiRunDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Equal(t, "api-parent-1", detail.ID)
	assert.Len(t, detail.ChildRuns, 2)

	childIDs := map[string]bool{}
	for _, c := range detail.ChildRuns {
		childIDs[c.ID] = true
		assert.Equal(t, "api-parent-1", c.ParentRunID)
	}
	assert.True(t, childIDs["api-child-1"])
	assert.True(t, childIDs["api-child-2"])
}

func TestAPIRuns_ParentRunID(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	parent := domain.NewRun("api-p-1", "main")
	parent.IsHost = true
	parent.Start()
	require.NoError(t, store.CreateRun(ctx, parent))

	child := domain.NewRun("api-c-1", "develop")
	child.ParentRunID = "api-p-1"
	child.Start()
	require.NoError(t, store.CreateRun(ctx, child))

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var runs []apiRun
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &runs))

	for _, r := range runs {
		if r.ID == "api-c-1" {
			assert.Equal(t, "api-p-1", r.ParentRunID)
		}
		if r.ID == "api-p-1" {
			assert.Equal(t, "", r.ParentRunID)
		}
	}
}

func TestRunsList_TreeGrouping(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create a parent host run
	parent := domain.NewRun("tree-parent-1", "main")
	parent.IsHost = true
	parent.ProjectDir = "/project"
	parent.Start()
	require.NoError(t, store.CreateRun(ctx, parent))

	// Create child run
	child := domain.NewRun("tree-child-1", "develop")
	child.ProjectDir = "/project"
	child.ParentRunID = "tree-parent-1"
	child.Start()
	require.NoError(t, store.CreateRun(ctx, child))

	req := httptest.NewRequest("GET", "/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Parent row should have run-parent-row class (host run without parent)
	assert.Contains(t, body, "run-parent-row")
	// Child row should have run-child-row class
	assert.Contains(t, body, "run-child-row")
}

func TestStepOutput_ReturnsStepLog(t *testing.T) {
	h, store := setupHandler(t)

	dir := t.TempDir()
	seedRunWithProject(t, store, "step-out-1", "develop", domain.RunStateSucceeded, dir)

	// Create step-specific log file
	outputDir := filepath.Join(dir, ".cloche", "step-out-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "implement.log"), []byte("step impl output"), 0644))

	req := httptest.NewRequest("GET", "/api/runs/step-out-1/steps/implement/output", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "step impl output")
}

func TestStepOutput_DoesNotFallBackToContainerLog(t *testing.T) {
	// Regression test: the web UI must NOT serve container.log as step output.
	// container.log contains unfiltered output from ALL steps, which causes
	// the web UI to show wrong/stale content for a specific step.
	h, store := setupHandler(t)

	dir := t.TempDir()
	seedRunWithProject(t, store, "step-out-2", "develop", domain.RunStateSucceeded, dir)

	// Create container.log but NO step-specific log
	outputDir := filepath.Join(dir, ".cloche", "step-out-2", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "container.log"), []byte("wrong: mixed output from all steps"), 0644))

	req := httptest.NewRequest("GET", "/api/runs/step-out-2/steps/implement/output", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should return 404, NOT the container.log content
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NotContains(t, w.Body.String(), "wrong: mixed output from all steps")
}

func TestStepOutput_DoesNotFallBackToLiveDockerLogs(t *testing.T) {
	// Even with a container manager that returns logs, step output should not
	// fall back to unfiltered live docker logs for a specific step.
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	mgr := newMockContainerManager()
	mgr.containers["abc123def456789"] = true
	mgr.running["abc123def456789"] = true

	h, err := NewHandler(store, store, WithContainerManager(mgr))
	require.NoError(t, err)

	dir := t.TempDir()
	seedRunWithProject(t, store, "step-out-3", "develop", domain.RunStateRunning, dir)

	// No step log, no container.log on disk — only live docker logs available
	req := httptest.NewRequest("GET", "/api/runs/step-out-3/steps/implement/output", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should return 404, NOT "mock logs" from the container manager
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NotContains(t, w.Body.String(), "mock logs")
}
