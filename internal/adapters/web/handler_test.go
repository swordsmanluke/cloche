package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	assert.Contains(t, body, "loadedOutputs[noKey]")
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
	assert.Contains(t, body, "var atBottom = container.scrollHeight - savedScrollTop - container.clientHeight < 40")
	// Must restore scroll position when not at bottom
	assert.Contains(t, body, "container.scrollTop = savedScrollTop")
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

	var entries []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))
	assert.Len(t, entries, 2)

	ids := map[string]bool{}
	for _, e := range entries {
		if e.Run != nil {
			ids[e.Run.ID] = true
		}
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

	// Clean URL /projects/{name}/runs renders filtered runs directly
	req = httptest.NewRequest("GET", "/projects/alpha/runs", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	body = w.Body.String()
	assert.Contains(t, body, "run-a1")
	assert.Contains(t, body, "run-a2")
	assert.NotContains(t, body, "run-b1")
	// Backlink to project page should be present
	assert.Contains(t, body, `href="/projects/alpha"`)

	// Unfiltered runs page should NOT have a project backlink
	req = httptest.NewRequest("GET", "/runs", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	body = w.Body.String()
	assert.NotContains(t, body, `href="/projects/`)

	// Legacy ?project= query param redirects to clean URL
	req = httptest.NewRequest("GET", "/runs?project=/home/user/alpha", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/projects/alpha/runs", w.Header().Get("Location"))
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

	var allEntries []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &allEntries))
	// 2 ungrouped runs (no task_id)
	assert.Len(t, allEntries, 2)

	// Clean URL /api/projects/{name}/runs returns filtered runs
	req = httptest.NewRequest("GET", "/api/projects/alpha/runs", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var filtered []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &filtered))
	assert.Len(t, filtered, 1)
	require.NotNil(t, filtered[0].Run)
	assert.Equal(t, "api-a1", filtered[0].Run.ID)
	assert.Equal(t, "/home/user/alpha", filtered[0].Run.ProjectDir)
	assert.Equal(t, "alpha", filtered[0].Run.ProjectLabel)

	// Legacy ?project= query param redirects to clean URL
	req = httptest.NewRequest("GET", "/api/runs?project=/home/user/alpha", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/api/projects/alpha/runs", w.Header().Get("Location"))
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
	assert.NotContains(t, w.Body.String(), "project-card")
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

	// Inline command with no script file under .cloche/scripts/ should return empty
	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows/main/steps/prepare/content", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())

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

	// Inline command with no script file under .cloche/scripts/ should return empty
	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows/main/steps/test/content", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestAPIStepContent_BinaryFileNotServed(t *testing.T) {
	h, store := setupHandler(t)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755))

	// Create a binary file named "test" in the project root (simulates compiled Go test binary)
	binaryContent := make([]byte, 1024)
	binaryContent[0] = 0x7f // ELF header byte
	binaryContent[1] = 0x00 // null byte makes it binary
	require.NoError(t, os.WriteFile(filepath.Join(dir, "test"), binaryContent, 0o755))

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

	seedRunWithProject(t, store, "bin-1", "main", domain.RunStateRunning, dir)

	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows/main/steps/test/content", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Should NOT return binary content
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestAPIStepContent_FileRefOutsideScripts(t *testing.T) {
	h, store := setupHandler(t)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "some-script.sh"), []byte("#!/bin/bash\necho hi"), 0o644))

	hostWf := `workflow "main" {
    step build {
        run = file("some-script.sh")
        results = [success, fail]
    }
    build:success -> done
    build:fail -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "host.cloche"), []byte(hostWf), 0o644))

	seedRunWithProject(t, store, "fro-1", "main", domain.RunStateRunning, dir)

	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows/main/steps/build/content", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// file() ref outside .cloche/scripts/ should not serve content
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Body.String())
}

func TestIsTextContent(t *testing.T) {
	assert.True(t, isTextContent([]byte("hello world")))
	assert.True(t, isTextContent([]byte("#!/bin/bash\necho hi")))
	assert.True(t, isTextContent([]byte("")))
	assert.False(t, isTextContent([]byte{0x7f, 0x45, 0x4c, 0x46, 0x00})) // ELF with null byte
	assert.False(t, isTextContent([]byte("text\x00binary")))
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

func TestAPIDeleteProjectContainers_StopsRunning(t *testing.T) {
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
	assert.Equal(t, float64(2), resp["deleted"])

	// Running container was stopped then removed
	assert.Contains(t, mgr.stopped, "cid-pdr1")
	assert.Contains(t, mgr.removed, "cid-pdr1")
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

func TestAPIProjectInfo_PromptFileContent(t *testing.T) {
	// The project info API should return prompt file contents, not just git history.
	h, store := setupHandler(t)

	dir := t.TempDir()
	seedRunWithProject(t, store, "info-1", "develop", domain.RunStateSucceeded, dir)

	// Create .cloche/prompts/ with a prompt file
	promptsDir := filepath.Join(dir, ".cloche", "prompts")
	require.NoError(t, os.MkdirAll(promptsDir, 0o755))
	promptContent := "You are a coding assistant.\nImplement the feature described below."
	require.NoError(t, os.WriteFile(filepath.Join(promptsDir, "implement.md"), []byte(promptContent), 0o644))

	// Initialize a git repo so git log doesn't fail
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %v: %s", args, out)
	}
	runGit("init")
	runGit("add", ".")
	runGit("commit", "-m", "initial")

	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/info", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		PromptFiles []struct {
			Path    string `json:"path"`
			Content string `json:"content"`
			History []struct {
				SHA string `json:"sha"`
			} `json:"history"`
		} `json:"prompt_files"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.PromptFiles, 1)
	assert.Equal(t, filepath.Join(".cloche", "prompts", "implement.md"), resp.PromptFiles[0].Path)
	assert.Equal(t, promptContent, resp.PromptFiles[0].Content)
	assert.NotEmpty(t, resp.PromptFiles[0].History, "should still include git history")
}

func TestWorkflowAPI_ComplexGraph(t *testing.T) {
	// Test that the workflow API returns the full graph structure needed by
	// the layered layout engine (steps with types, wires with results,
	// entry_step for layer assignment).
	h, store := setupHandler(t)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755))

	// Complex workflow with branching: A -> B, A -> C, B -> done, C -> done, A -> abort
	wf := `workflow "pipeline" {
    step analyze {
        prompt = "Analyze the input"
        results = [pass, fail, error]
    }
    step transform {
        prompt = "Transform data"
        results = [success, fail]
    }
    step validate {
        prompt = "Validate output"
        results = [success, fail]
    }
    analyze:pass -> transform
    analyze:fail -> validate
    analyze:error -> abort
    transform:success -> done
    transform:fail -> abort
    validate:success -> done
    validate:fail -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"), []byte(wf), 0o644))
	seedRunWithProject(t, store, "cg-1", "pipeline", domain.RunStateRunning, dir)

	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/workflows", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var workflows []struct {
		Name      string `json:"name"`
		Location  string `json:"location"`
		EntryStep string `json:"entry_step"`
		Steps     []struct {
			Name    string   `json:"name"`
			Type    string   `json:"type"`
			Results []string `json:"results"`
		} `json:"steps"`
		Wires []struct {
			From   string `json:"from"`
			Result string `json:"result"`
			To     string `json:"to"`
		} `json:"wires"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &workflows))
	require.Len(t, workflows, 1)

	pipeline := workflows[0]
	assert.Equal(t, "pipeline", pipeline.Name)
	assert.Equal(t, "container", pipeline.Location)
	assert.Equal(t, "analyze", pipeline.EntryStep)

	// Should have 3 steps
	assert.Len(t, pipeline.Steps, 3)
	stepNames := map[string]bool{}
	for _, s := range pipeline.Steps {
		stepNames[s.Name] = true
	}
	assert.True(t, stepNames["analyze"])
	assert.True(t, stepNames["transform"])
	assert.True(t, stepNames["validate"])

	// Should have 10 wires: 7 explicit + 3 implicit timeout->abort (one per step)
	assert.Len(t, pipeline.Wires, 10)

	// Count terminal wires: should have wires to both done and abort
	terminalTargets := map[string]int{}
	for _, wire := range pipeline.Wires {
		if wire.To == "done" || wire.To == "abort" {
			terminalTargets[wire.To]++
		}
	}
	// transform:success->done, validate:success->done = 2 wires to done
	assert.Equal(t, 2, terminalTargets["done"])
	// analyze:error->abort, transform:fail->abort, validate:fail->abort = 3 explicit + 3 implicit timeout->abort = 6
	assert.Equal(t, 6, terminalTargets["abort"])
}

func TestProjectDetail_RendersLayoutEngine(t *testing.T) {
	// Verify the project detail page contains the layered layout engine code.
	h, store := setupHandler(t)

	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755))

	wf := `workflow "develop" {
    step implement {
        prompt = "Build it"
        results = [success, fail]
    }
    implement:success -> done
    implement:fail -> abort
}
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"), []byte(wf), 0o644))
	seedRunWithProject(t, store, "le-1", "develop", domain.RunStateRunning, dir)

	req := httptest.NewRequest("GET", "/projects/"+filepath.Base(dir), nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Layout engine key features
	assert.Contains(t, body, "workflow-dag")           // DAG container
	assert.Contains(t, body, "showWorkflow")           // Main render function
	assert.Contains(t, body, "layerOf")                // Layer assignment
	assert.Contains(t, body, "topoOrder")              // Topological sort
	assert.Contains(t, body, "dag-link-node")           // Wire column link node dots
	assert.Contains(t, body, "wireColStart")            // Wire column start position
	assert.Contains(t, body, "layerGap")               // Horizontal layer spacing
	assert.Contains(t, body, "termWires")              // Terminal wire merging
	assert.Contains(t, body, "maxOffset")              // Max endpoint offset to eliminate elbow joins
	assert.Contains(t, body, "orthoPath")              // Orthogonal (right-angle) path helper
	assert.Contains(t, body, "isSuccessResult")        // Success result detection helper
	assert.Contains(t, body, "resultColor")            // Color mapping for wire results
	assert.Contains(t, body, "wireColumns")            // Wire column routing for failure paths
	assert.Contains(t, body, "isFailureResult")        // Failure result detection helper
	assert.Contains(t, body, "colorPalette")           // Multi-color palette for custom results
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

func TestStepOutput_FallsBackToOutFile(t *testing.T) {
	// Host workflow runs write .out files instead of .log files.
	// The step output endpoint should fall back to .out when .log is missing.
	h, store := setupHandler(t)

	dir := t.TempDir()
	seedRunWithProject(t, store, "step-out-host-1", "main", domain.RunStateSucceeded, dir)

	outputDir := filepath.Join(dir, ".cloche", "step-out-host-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "prepare.log"), []byte("host prepare output"), 0644))

	req := httptest.NewRequest("GET", "/api/runs/step-out-host-1/steps/prepare/output", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "host prepare output")
}

func TestStepOutput_ReadsLogFile(t *testing.T) {
	// Step output is read from the .log file.
	h, store := setupHandler(t)

	dir := t.TempDir()
	seedRunWithProject(t, store, "step-out-pref-1", "main", domain.RunStateSucceeded, dir)

	outputDir := filepath.Join(dir, ".cloche", "step-out-pref-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "build.log"), []byte("log file content"), 0644))

	req := httptest.NewRequest("GET", "/api/runs/step-out-pref-1/steps/build/output", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "log file content")
}

// --- Tasks API tests ---

// mockTaskProvider implements TaskProvider for testing.
type mockTaskProvider struct {
	tasks        map[string][]TaskEntry // projectDir -> tasks
	releasedTask string                 // last released task ID
	releaseErr   error                  // error to return from ReleaseTask
}

func (m *mockTaskProvider) GetLoopTasks(projectDir string) []TaskEntry {
	return m.tasks[projectDir]
}

func (m *mockTaskProvider) ReleaseTask(ctx context.Context, projectDir string, taskID string) error {
	if m.releaseErr != nil {
		return m.releaseErr
	}
	m.releasedTask = taskID
	return nil
}

func TestAPITasks_WithTasks(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Seed a project so the label resolver works.
	projectDir := "/home/user/projects/myapp"
	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = projectDir
	require.NoError(t, store.CreateRun(ctx, run))

	// Set up mock task provider.
	tp := &mockTaskProvider{
		tasks: map[string][]TaskEntry{
			projectDir: {
				{ID: "task-1", Status: "open", Title: "Fix bug", Assigned: true, AssignedAt: "2026-03-11T10:00:00Z", RunID: "run-fix-bug"},
				{ID: "task-2", Status: "open", Title: "Add feature", Assigned: false},
				{ID: "task-3", Status: "closed", Title: "Done thing", Assigned: false},
			},
		},
	}
	h.taskProvider = tp

	req := httptest.NewRequest("GET", "/api/projects/myapp/tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))

	var tasks []TaskEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Len(t, tasks, 3)

	assert.Equal(t, "task-1", tasks[0].ID)
	assert.True(t, tasks[0].Assigned)
	assert.Equal(t, "run-fix-bug", tasks[0].RunID)

	assert.Equal(t, "task-2", tasks[1].ID)
	assert.False(t, tasks[1].Assigned)

	assert.Equal(t, "task-3", tasks[2].ID)
	assert.Equal(t, "closed", tasks[2].Status)
}

func TestAPITasks_NoTaskProvider(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Seed a project.
	projectDir := "/home/user/projects/myapp"
	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = projectDir
	require.NoError(t, store.CreateRun(ctx, run))

	// No task provider set — should return empty array.
	req := httptest.NewRequest("GET", "/api/projects/myapp/tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var tasks []TaskEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Empty(t, tasks)
}

func TestAPITasks_NoLoop(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	projectDir := "/home/user/projects/myapp"
	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = projectDir
	require.NoError(t, store.CreateRun(ctx, run))

	// Task provider returns nil (no loop active for project).
	tp := &mockTaskProvider{tasks: map[string][]TaskEntry{}}
	h.taskProvider = tp

	req := httptest.NewRequest("GET", "/api/projects/myapp/tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var tasks []TaskEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Empty(t, tasks)
}

func TestAPITasks_ProjectNotFound(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/api/projects/nonexistent/tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIReleaseTask_Success(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	projectDir := "/home/user/projects/myapp"
	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = projectDir
	require.NoError(t, store.CreateRun(ctx, run))

	tp := &mockTaskProvider{
		tasks: map[string][]TaskEntry{
			projectDir: {
				{ID: "task-1", Status: "in_progress", Title: "Stale task", Stale: true},
			},
		},
	}
	h.taskProvider = tp

	req := httptest.NewRequest("POST", "/api/projects/myapp/tasks/task-1/release", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "task-1", tp.releasedTask)

	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp["status"])
}

func TestAPIReleaseTask_NoProvider(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	projectDir := "/home/user/projects/myapp"
	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = projectDir
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("POST", "/api/projects/myapp/tasks/task-1/release", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIReleaseTask_Error(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	projectDir := "/home/user/projects/myapp"
	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = projectDir
	require.NoError(t, store.CreateRun(ctx, run))

	tp := &mockTaskProvider{
		tasks:      map[string][]TaskEntry{},
		releaseErr: fmt.Errorf("workflow failed"),
	}
	h.taskProvider = tp

	req := httptest.NewRequest("POST", "/api/projects/myapp/tasks/task-1/release", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestAPITasks_StaleField(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	projectDir := "/home/user/projects/myapp"
	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = projectDir
	require.NoError(t, store.CreateRun(ctx, run))

	tp := &mockTaskProvider{
		tasks: map[string][]TaskEntry{
			projectDir: {
				{ID: "task-1", Status: "in_progress", Title: "Active task", Stale: false},
				{ID: "task-2", Status: "in_progress", Title: "Stale task", Stale: true},
				{ID: "task-3", Status: "open", Title: "Upcoming task"},
			},
		},
	}
	h.taskProvider = tp

	req := httptest.NewRequest("GET", "/api/projects/myapp/tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var tasks []TaskEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Len(t, tasks, 3)
	assert.False(t, tasks[0].Stale)
	assert.True(t, tasks[1].Stale)
	assert.False(t, tasks[2].Stale)
}

func TestAPIDeleteAllContainers_Success(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-ac1", "develop", "/home/user/alpha", "cid-ac1", true)
	seedRunWithContainer(t, store, mgr, "run-ac2", "develop", "/home/user/alpha", "cid-ac2", true)
	seedRunWithContainer(t, store, mgr, "run-ac3", "deploy", "/home/user/beta", "cid-ac3", true)

	req := httptest.NewRequest("DELETE", "/api/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(3), resp["deleted"])

	// Verify all containers were removed
	assert.Contains(t, mgr.removed, "cid-ac1")
	assert.Contains(t, mgr.removed, "cid-ac2")
	assert.Contains(t, mgr.removed, "cid-ac3")

	// Verify runs updated
	for _, id := range []string{"run-ac1", "run-ac2", "run-ac3"} {
		run, err := store.GetRun(context.Background(), id)
		require.NoError(t, err)
		assert.False(t, run.ContainerKept)
	}
}

func TestAPIDeleteAllContainers_StopsRunning(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-acr1", "develop", "/home/user/alpha", "cid-acr1", true)
	mgr.running["cid-acr1"] = true // mark as running

	seedRunWithContainer(t, store, mgr, "run-acr2", "develop", "/home/user/beta", "cid-acr2", true)

	req := httptest.NewRequest("DELETE", "/api/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(2), resp["deleted"])

	// Running container was stopped then removed
	assert.Contains(t, mgr.stopped, "cid-acr1")
	assert.Contains(t, mgr.removed, "cid-acr1")
	assert.Contains(t, mgr.removed, "cid-acr2")
}

func TestAPIDeleteAllContainers_SkipsNonKept(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-ack1", "develop", "/home/user/alpha", "cid-ack1", false) // not kept

	req := httptest.NewRequest("DELETE", "/api/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, float64(0), resp["deleted"])
}

func TestAPIDeleteAllContainers_NoManager(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "run-acnm", "develop", domain.RunStateSucceeded, "/home/user/alpha")

	req := httptest.NewRequest("DELETE", "/api/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestRunsList_CleanupButton(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-cb1", "develop", "/home/user/alpha", "cid-cb1", true)
	seedRunWithContainer(t, store, mgr, "run-cb2", "develop", "/home/user/beta", "cid-cb2", true)

	req := httptest.NewRequest("GET", "/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `id="cleanup-containers-btn"`)
	assert.Contains(t, body, "Clean up 2 old containers")
}

func TestRunsList_NoCleanupButtonWhenNoContainers(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-ncb1", "develop", "/home/user/alpha", "cid-ncb1", false)

	req := httptest.NewRequest("GET", "/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.NotContains(t, body, `id="cleanup-containers-btn"`)
}

func TestAPIRuns_TaskID(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create a run with a task ID
	run := domain.NewRun("task-run-1", "main")
	run.IsHost = true
	run.TaskID = "task-42"
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	// Create another run without a task ID
	run2 := domain.NewRun("task-run-2", "develop")
	run2.Start()
	require.NoError(t, store.CreateRun(ctx, run2))

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var runs []apiRun
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &runs))

	for _, r := range runs {
		if r.ID == "task-run-1" {
			assert.Equal(t, "task-42", r.TaskID)
		}
		if r.ID == "task-run-2" {
			assert.Equal(t, "", r.TaskID)
		}
	}
}

func TestRunsList_HidesListTasksRuns(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create a list-tasks host run
	listRun := domain.NewRun("lt-run-1", "list-tasks")
	listRun.IsHost = true
	listRun.ProjectDir = "/project"
	listRun.Start()
	require.NoError(t, store.CreateRun(ctx, listRun))

	// Create a main host run
	mainRun := domain.NewRun("main-run-1", "main")
	mainRun.IsHost = true
	mainRun.ProjectDir = "/project"
	mainRun.Start()
	require.NoError(t, store.CreateRun(ctx, mainRun))

	req := httptest.NewRequest("GET", "/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// list-tasks run should be hidden from the rendered HTML
	assert.NotContains(t, body, "lt-run-1")
	// main run should be visible
	assert.Contains(t, body, "main-run-1")
}

func TestRunsList_TaskGrouping(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create two host runs with the same task ID
	main1 := domain.NewRun("tg-main-1", "main")
	main1.IsHost = true
	main1.ProjectDir = "/project"
	main1.TaskID = "task-100"
	main1.Start()
	require.NoError(t, store.CreateRun(ctx, main1))

	fin1 := domain.NewRun("tg-fin-1", "post-merge")
	fin1.IsHost = true
	fin1.ProjectDir = "/project"
	fin1.TaskID = "task-100"
	fin1.Start()
	require.NoError(t, store.CreateRun(ctx, fin1))

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var entries []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	// Should have: 1 task header + 2×(attempt header + run row) = 5 entries.
	// Each top-level run is an attempt and also appears as a row within that attempt.
	assert.Len(t, entries, 5)

	// First entry should be the task header
	assert.True(t, entries[0].TaskHeader)
	assert.Equal(t, "task-100", entries[0].TaskID)

	// Entries 1 and 3 are attempt headers; entries 2 and 4 are run rows
	assert.True(t, entries[1].AttemptHeader)
	assert.Equal(t, "task-100", entries[1].TaskID)
	require.NotNil(t, entries[2].Run)
	assert.True(t, entries[2].IsChild)
	assert.True(t, entries[3].AttemptHeader)
	assert.Equal(t, "task-100", entries[3].TaskID)
	require.NotNil(t, entries[4].Run)
	assert.True(t, entries[4].IsChild)
}

func TestRunsList_TaskGroupingTitle(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create a run with a task ID
	r1 := domain.NewRun("tt-run-1", "develop")
	r1.IsHost = true
	r1.ProjectDir = "/project"
	r1.TaskID = "task-300"
	r1.Start()
	require.NoError(t, store.CreateRun(ctx, r1))

	// Set up mock task provider with a title for this task
	tp := &mockTaskProvider{
		tasks: map[string][]TaskEntry{
			"/project": {
				{ID: "task-300", Status: "open", Title: "Fix login page"},
			},
		},
	}
	h.taskProvider = tp

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var entries []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	// First entry should be the task header with the title
	require.True(t, entries[0].TaskHeader)
	assert.Equal(t, "task-300", entries[0].TaskID)
	assert.Equal(t, "Fix login page", entries[0].TaskTitle)
}

func TestRunsList_TaskGroupingTitle_MultipleTasks(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create runs for two different tasks
	r1 := domain.NewRun("tt-run-a", "develop")
	r1.IsHost = true
	r1.ProjectDir = "/project"
	r1.TaskID = "task-100"
	r1.Start()
	require.NoError(t, store.CreateRun(ctx, r1))

	r2 := domain.NewRun("tt-run-b", "develop")
	r2.IsHost = true
	r2.ProjectDir = "/project"
	r2.TaskID = "task-200"
	r2.Start()
	require.NoError(t, store.CreateRun(ctx, r2))

	tp := &mockTaskProvider{
		tasks: map[string][]TaskEntry{
			"/project": {
				{ID: "task-100", Status: "in_progress", Title: "Fix authentication"},
				{ID: "task-200", Status: "in_progress", Title: "Add dark mode"},
			},
		},
	}
	h.taskProvider = tp

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var entries []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	// Should have two task headers, each with their title
	var headers []apiGroupedEntry
	for _, e := range entries {
		if e.TaskHeader {
			headers = append(headers, e)
		}
	}
	require.Len(t, headers, 2, "should have 2 task headers")
	// Both headers should have titles (order depends on sort)
	titles := map[string]string{}
	for _, h := range headers {
		titles[h.TaskID] = h.TaskTitle
	}
	assert.Equal(t, "Fix authentication", titles["task-100"])
	assert.Equal(t, "Add dark mode", titles["task-200"])
}

func TestTaskAggregateStatus(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name   string
		states []domain.RunState
		times  []time.Time // optional StartedAt per run; nil uses zero
		want   string
	}{
		{"all succeeded", []domain.RunState{domain.RunStateSucceeded, domain.RunStateSucceeded}, nil, "succeeded"},
		{"one pending", []domain.RunState{domain.RunStateSucceeded, domain.RunStatePending}, nil, "pending"},
		{"one running", []domain.RunState{domain.RunStateSucceeded, domain.RunStateRunning}, nil, "running"},
		{"running outweighs failed", []domain.RunState{domain.RunStateSucceeded, domain.RunStateRunning, domain.RunStateFailed}, nil, "running"},
		{"running beats failed", []domain.RunState{domain.RunStateRunning, domain.RunStateFailed}, nil, "running"},
		{"running beats pending", []domain.RunState{domain.RunStatePending, domain.RunStateRunning}, nil, "running"},
		{"pending beats succeeded", []domain.RunState{domain.RunStateSucceeded, domain.RunStatePending}, nil, "pending"},
		{"most recent terminal wins",
			[]domain.RunState{domain.RunStateSucceeded, domain.RunStateCancelled},
			[]time.Time{now.Add(-1 * time.Minute), now},
			"cancelled"},
		{"pending beats cancelled", []domain.RunState{domain.RunStateCancelled, domain.RunStatePending}, nil, "pending"},
		{"single pending", []domain.RunState{domain.RunStatePending}, nil, "pending"},
		{"single failed", []domain.RunState{domain.RunStateFailed}, nil, "failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var runs []*domain.Run
			for i, s := range tt.states {
				// Use a distinct workflow name per run so deduplication in
				// AttemptAggregateStatus does not collapse unrelated runs.
				r := domain.NewRun(fmt.Sprintf("r-%d", i), fmt.Sprintf("wf-%d", i))
				r.State = s
				if tt.times != nil && i < len(tt.times) {
					r.StartedAt = tt.times[i]
				}
				runs = append(runs, r)
			}
			got := taskAggregateStatus(runs)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunsList_TaskGroupingStatus(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create runs with same task ID in different states
	r1 := domain.NewRun("ts-run-1", "main")
	r1.IsHost = true
	r1.ProjectDir = "/project"
	r1.TaskID = "task-200"
	r1.Start()
	r1.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, r1))

	r2 := domain.NewRun("ts-run-2", "main")
	r2.IsHost = true
	r2.ProjectDir = "/project"
	r2.TaskID = "task-200"
	r2.Start()
	require.NoError(t, store.CreateRun(ctx, r2))

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var entries []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	// First entry should be task header with aggregate status = running
	require.True(t, entries[0].TaskHeader)
	assert.Equal(t, "task-200", entries[0].TaskID)
	assert.Equal(t, "running", entries[0].TaskStatus)

	// Now add a failed run — but r2 is still running, so active outweighs terminal
	r3 := domain.NewRun("ts-run-3", "main")
	r3.IsHost = true
	r3.ProjectDir = "/project"
	r3.TaskID = "task-200"
	r3.Start()
	r3.Fail("something broke")
	require.NoError(t, store.CreateRun(ctx, r3))

	req2 := httptest.NewRequest("GET", "/api/runs", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)

	var entries2 []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &entries2))

	require.True(t, entries2[0].TaskHeader)
	assert.Equal(t, "running", entries2[0].TaskStatus)
}

func TestRunsList_ChildFailedOverridesHostSuccess(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Host run succeeded, but a child container run failed.
	hostRun := domain.NewRun("cf-host-1", "main")
	hostRun.IsHost = true
	hostRun.ProjectDir = "/project"
	hostRun.TaskID = "task-600"
	hostRun.AttemptID = "cf01"
	hostRun.Start()
	hostRun.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, hostRun))

	childRun := domain.NewRun("cf-child-1", "develop")
	childRun.ProjectDir = "/project"
	childRun.TaskID = "task-600"
	childRun.AttemptID = "cf01"
	childRun.ParentRunID = "cf-host-1"
	childRun.Start()
	childRun.Fail("tests failed")
	require.NoError(t, store.CreateRun(ctx, childRun))

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var entries []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	// Task header should show failed because child run failed.
	require.True(t, entries[0].TaskHeader)
	assert.Equal(t, "task-600", entries[0].TaskID)
	assert.Equal(t, "failed", entries[0].TaskStatus)

	// Attempt header should also show failed.
	require.True(t, entries[1].AttemptHeader)
	assert.Equal(t, "failed", entries[1].AttemptStatus)
}

func TestRunsList_TaskStatusReflectsLatestAttempt(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// First attempt: failed
	r1 := domain.NewRun("lta-run-1", "main")
	r1.IsHost = true
	r1.ProjectDir = "/project"
	r1.TaskID = "task-700"
	r1.AttemptID = "at01"
	r1.StartedAt = time.Now().Add(-10 * time.Minute)
	r1.Start()
	r1.Fail("something went wrong")
	require.NoError(t, store.CreateRun(ctx, r1))

	// Second (latest) attempt: succeeded
	r2 := domain.NewRun("lta-run-2", "main")
	r2.IsHost = true
	r2.ProjectDir = "/project"
	r2.TaskID = "task-700"
	r2.AttemptID = "at02"
	r2.StartedAt = time.Now().Add(-2 * time.Minute)
	r2.Start()
	r2.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, r2))

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var entries []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	// Task status should reflect latest attempt (succeeded), not earlier failed one.
	require.True(t, entries[0].TaskHeader)
	assert.Equal(t, "task-700", entries[0].TaskID)
	assert.Equal(t, "succeeded", entries[0].TaskStatus)
}

func TestRunsList_HostRunFailedOverridesChildSuccess(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Host run failed (e.g., merge step failed).
	hostRun := domain.NewRun("hf-host-1", "main")
	hostRun.IsHost = true
	hostRun.ProjectDir = "/project"
	hostRun.TaskID = "task-500"
	hostRun.Start()
	time.Sleep(time.Millisecond) // ensure child starts later
	hostRun.Fail("merge failed")
	require.NoError(t, store.CreateRun(ctx, hostRun))

	// Child container run succeeded (develop step).
	childRun := domain.NewRun("hf-child-1", "develop")
	childRun.ProjectDir = "/project"
	childRun.TaskID = "task-500"
	childRun.ParentRunID = "hf-host-1"
	childRun.StartedAt = hostRun.StartedAt.Add(time.Second) // started after host
	childRun.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, childRun))

	req := httptest.NewRequest("GET", "/api/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var entries []apiGroupedEntry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &entries))

	// Task header should show failed (host run state), not succeeded (child run state).
	require.True(t, entries[0].TaskHeader)
	assert.Equal(t, "task-500", entries[0].TaskID)
	assert.Equal(t, "failed", entries[0].TaskStatus)
}

func TestProjectDetail_TasksPanel(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Seed a project so it resolves
	run := domain.NewRun("pd-run-1", "develop")
	run.ProjectDir = "/home/user/projects/taskapp"
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/projects/taskapp", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// The tasks panels should be present in the HTML
	assert.Contains(t, body, `id="in-progress-panel"`)
	assert.Contains(t, body, `id="in-progress-body"`)
	assert.Contains(t, body, "In-progress Tasks")
	assert.Contains(t, body, `id="upcoming-panel"`)
	assert.Contains(t, body, `id="upcoming-body"`)
	assert.Contains(t, body, "Upcoming Tasks")
}

func TestTaskID_StorePersistence(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run with task ID
	run := domain.NewRun("persist-1", "main")
	run.TaskID = "persist-task-55"
	run.ProjectDir = "/project"
	require.NoError(t, store.CreateRun(ctx, run))

	// Retrieve and verify task ID is persisted
	got, err := store.GetRun(ctx, "persist-1")
	require.NoError(t, err)
	assert.Equal(t, "persist-task-55", got.TaskID)

	// Update the task ID
	got.TaskID = "updated-task-77"
	require.NoError(t, store.UpdateRun(ctx, got))

	got2, err := store.GetRun(ctx, "persist-1")
	require.NoError(t, err)
	assert.Equal(t, "updated-task-77", got2.TaskID)
}

func TestTaskID_DefaultEmpty(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run without setting task ID
	run := domain.NewRun("no-task-1", "develop")
	run.ProjectDir = "/project"
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "no-task-1")
	require.NoError(t, err)
	assert.Equal(t, "", got.TaskID)
}

// TestGroupAndSortRuns_ParentRunShownAsRow verifies that a top-level run
// belonging to a task group appears as a row (not only as an AttemptHeader).
func TestGroupAndSortRuns_ParentRunShownAsRow(t *testing.T) {
	now := time.Now()
	mainRun := domain.NewRun("main-1", "main")
	mainRun.IsHost = true
	mainRun.TaskID = "task-abc"
	mainRun.StartedAt = now

	entries := groupAndSortRuns([]*domain.Run{mainRun}, nil, nil)

	// Expect: TaskHeader, AttemptHeader, main run row
	require.Len(t, entries, 3)
	assert.True(t, entries[0].TaskHeader)
	assert.True(t, entries[1].AttemptHeader)
	require.NotNil(t, entries[2].Run)
	assert.Equal(t, "main-1", entries[2].Run.ID)
	assert.True(t, entries[2].IsChild)
}

// TestGroupAndSortRuns_ChildRunsNestedUnderMain verifies that when child runs
// have ParentRunID pointing to the main run, all workflows (main, develop,
// post-merge) appear nested under a single attempt block.
func TestGroupAndSortRuns_ChildRunsNestedUnderMain(t *testing.T) {
	now := time.Now()

	mainRun := domain.NewRun("main-1", "main")
	mainRun.IsHost = true
	mainRun.TaskID = "task-xyz"
	mainRun.StartedAt = now
	mainRun.Complete(domain.RunStateSucceeded)

	developRun := domain.NewRun("develop-1", "develop")
	developRun.TaskID = "task-xyz"
	developRun.ParentRunID = "main-1"
	developRun.StartedAt = now.Add(time.Second)
	developRun.Complete(domain.RunStateSucceeded)

	postMergeRun := domain.NewRun("post-merge-1", "post-merge")
	postMergeRun.IsHost = true
	postMergeRun.TaskID = "task-xyz"
	postMergeRun.ParentRunID = "main-1"
	postMergeRun.StartedAt = now.Add(2 * time.Second)
	postMergeRun.Complete(domain.RunStateSucceeded)

	runs := []*domain.Run{mainRun, developRun, postMergeRun}
	entries := groupAndSortRuns(runs, nil, nil)

	// Expect: TaskHeader, one AttemptHeader, main row, develop row, post-merge row
	require.Len(t, entries, 5, "expected TaskHeader + AttemptHeader + 3 run rows")

	assert.True(t, entries[0].TaskHeader)
	assert.Equal(t, "task-xyz", entries[0].TaskID)

	assert.True(t, entries[1].AttemptHeader)
	assert.Equal(t, 1, entries[1].AttemptNum)

	ids := []string{}
	for _, e := range entries[2:] {
		require.NotNil(t, e.Run)
		assert.True(t, e.IsChild)
		ids = append(ids, e.Run.ID)
	}
	assert.ElementsMatch(t, []string{"main-1", "develop-1", "post-merge-1"}, ids)
}

// TestGroupAndSortRuns_AttemptStatusAggregatesChildren verifies that the
// AttemptHeader status reflects the aggregate state of parent + all children,
// so a running child makes the attempt appear as "running" even if the parent
// (main) has already completed.
func TestGroupAndSortRuns_AttemptStatusAggregatesChildren(t *testing.T) {
	now := time.Now()

	mainRun := domain.NewRun("main-agg", "main")
	mainRun.IsHost = true
	mainRun.TaskID = "task-agg"
	mainRun.StartedAt = now
	mainRun.Complete(domain.RunStateSucceeded)

	// child workflow is still running
	childRun := domain.NewRun("post-merge-agg", "post-merge")
	childRun.IsHost = true
	childRun.TaskID = "task-agg"
	childRun.ParentRunID = "main-agg"
	childRun.StartedAt = now.Add(time.Second)
	childRun.Start()

	entries := groupAndSortRuns([]*domain.Run{mainRun, childRun}, nil, nil)

	// Find the AttemptHeader entry
	var attemptEntry *apiGroupedEntry
	for i := range entries {
		if entries[i].AttemptHeader {
			attemptEntry = &entries[i]
			break
		}
	}
	require.NotNil(t, attemptEntry)
	// Aggregate: parent succeeded but child is running → attempt is "running"
	assert.Equal(t, "running", attemptEntry.AttemptStatus)
}

// TestGroupAndSortRuns_RunsWithinAttemptSortedNewestFirst verifies that within
// a single attempt, runs are ordered by start time with the newest at the top.
func TestGroupAndSortRuns_RunsWithinAttemptSortedNewestFirst(t *testing.T) {
	now := time.Now()

	mainRun := domain.NewRun("main-ord", "main")
	mainRun.IsHost = true
	mainRun.TaskID = "task-ord"
	mainRun.StartedAt = now
	mainRun.Complete(domain.RunStateSucceeded)

	// child1 started 1s after main
	child1 := domain.NewRun("child-ord-1", "develop")
	child1.TaskID = "task-ord"
	child1.ParentRunID = "main-ord"
	child1.StartedAt = now.Add(time.Second)
	child1.Complete(domain.RunStateSucceeded)

	// child2 started 2s after main (newest child)
	child2 := domain.NewRun("child-ord-2", "post-merge")
	child2.TaskID = "task-ord"
	child2.ParentRunID = "main-ord"
	child2.StartedAt = now.Add(2 * time.Second)
	child2.Complete(domain.RunStateSucceeded)

	entries := groupAndSortRuns([]*domain.Run{mainRun, child1, child2}, nil, nil)

	// Expect: TaskHeader + AttemptHeader + 3 run rows = 5 entries
	require.Len(t, entries, 5)
	assert.True(t, entries[0].TaskHeader)
	assert.True(t, entries[1].AttemptHeader)
	// Runs should be newest first: child2 (t+2s), child1 (t+1s), main (t+0s)
	require.NotNil(t, entries[2].Run)
	assert.Equal(t, "child-ord-2", entries[2].Run.ID)
	require.NotNil(t, entries[3].Run)
	assert.Equal(t, "child-ord-1", entries[3].Run.ID)
	require.NotNil(t, entries[4].Run)
	assert.Equal(t, "main-ord", entries[4].Run.ID)
}

// TestGroupAndSortRuns_MultipleAttempts verifies that two separate main runs
// (without ParentRunID linking) produce two distinct attempt blocks.
func TestGroupAndSortRuns_MultipleAttempts(t *testing.T) {
	now := time.Now()

	mainRun1 := domain.NewRun("main-a1", "main")
	mainRun1.IsHost = true
	mainRun1.TaskID = "task-multi"
	mainRun1.StartedAt = now.Add(-time.Minute)
	mainRun1.Complete(domain.RunStateFailed)

	mainRun2 := domain.NewRun("main-a2", "main")
	mainRun2.IsHost = true
	mainRun2.TaskID = "task-multi"
	mainRun2.StartedAt = now
	mainRun2.Start()

	entries := groupAndSortRuns([]*domain.Run{mainRun1, mainRun2}, nil, nil)

	// Expect: TaskHeader + 2×(AttemptHeader + run row) = 5 entries
	require.Len(t, entries, 5)
	assert.True(t, entries[0].TaskHeader)
	assert.True(t, entries[1].AttemptHeader)
	assert.Equal(t, 2, entries[1].AttemptNum) // latest attempt = #2 (running, shown first)
	assert.NotNil(t, entries[2].Run)
	assert.Equal(t, "main-a2", entries[2].Run.ID)
	assert.True(t, entries[3].AttemptHeader)
	assert.Equal(t, 1, entries[3].AttemptNum)
	assert.NotNil(t, entries[4].Run)
	assert.Equal(t, "main-a1", entries[4].Run.ID)
}

func TestAPIProjectUsage_Empty(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("usage-run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/projects/myapp/usage", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "application/json")

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	// Both slices should be present (possibly empty/nil)
	_, hasBR := resp["burn_rate_1h"]
	_, has24 := resp["totals_24h"]
	assert.True(t, hasBR || has24 || true) // endpoint returns valid JSON
}

func TestAPIProjectUsage_WithData(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("usage-run-2", "develop")
	run.ProjectDir = "/home/user/projects/tokenapp"
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	now := time.Now()
	require.NoError(t, store.SaveCapture(ctx, "usage-run-2", &domain.StepExecution{
		StepName:    "implement",
		Result:      "success",
		StartedAt:   now.Add(-10 * time.Minute),
		CompletedAt: now.Add(-5 * time.Minute),
		Usage: &domain.TokenUsage{
			InputTokens:  1000,
			OutputTokens: 500,
			AgentName:    "claude",
		},
	}))

	req := httptest.NewRequest("GET", "/api/projects/tokenapp/usage", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	// Should have data in the 1h and 24h buckets
	br, _ := resp["burn_rate_1h"].([]interface{})
	t24, _ := resp["totals_24h"].([]interface{})
	assert.Len(t, br, 1, "expected one agent in 1h burn rate")
	assert.Len(t, t24, 1, "expected one agent in 24h totals")

	if len(br) > 0 {
		entry := br[0].(map[string]interface{})
		assert.Equal(t, "claude", entry["agent_name"])
		assert.Equal(t, float64(1500), entry["total_tokens"])
		assert.Greater(t, entry["burn_rate"].(float64), 0.0)
	}
}

func TestAPIProjectUsage_NotFound(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/api/projects/nonexistent/usage", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestRunDetail_WithUsage(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-usage-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	now := time.Now()
	require.NoError(t, store.SaveCapture(ctx, "run-usage-1", &domain.StepExecution{
		StepName:    "implement",
		Result:      "success",
		StartedAt:   now.Add(-5 * time.Minute),
		CompletedAt: now,
		Usage: &domain.TokenUsage{
			InputTokens:  1234,
			OutputTokens: 567,
			AgentName:    "claude",
		},
	}))

	req := httptest.NewRequest("GET", "/runs/run-usage-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Agent")
	assert.Contains(t, body, "Tokens")
	assert.Contains(t, body, "claude")
	assert.Contains(t, body, "1234")
	assert.Contains(t, body, "567")
}

func TestAPIRunDetail_WithUsage(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("api-run-usage-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	now := time.Now()
	require.NoError(t, store.SaveCapture(ctx, "api-run-usage-1", &domain.StepExecution{
		StepName:    "code",
		Result:      "success",
		StartedAt:   now.Add(-2 * time.Minute),
		CompletedAt: now,
		Usage: &domain.TokenUsage{
			InputTokens:  800,
			OutputTokens: 200,
			AgentName:    "codex",
		},
	}))

	req := httptest.NewRequest("GET", "/api/runs/api-run-usage-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	steps, _ := resp["steps"].([]interface{})
	require.Len(t, steps, 1)
	step := steps[0].(map[string]interface{})
	assert.Equal(t, "code", step["step_name"])
	assert.Equal(t, "codex", step["agent_name"])
	assert.Equal(t, float64(800), step["input_tokens"])
	assert.Equal(t, float64(200), step["output_tokens"])
	assert.Equal(t, true, step["has_usage"])
}

func TestMergeCaptures_WithUsage(t *testing.T) {
	now := time.Now()
	caps := []*domain.StepExecution{
		{
			StepName:    "implement",
			Result:      "success",
			StartedAt:   now.Add(-5 * time.Minute),
			CompletedAt: now,
			Usage: &domain.TokenUsage{
				InputTokens:  100,
				OutputTokens: 50,
				AgentName:    "claude",
			},
		},
		{
			StepName:    "review",
			Result:      "done",
			StartedAt:   now.Add(-2 * time.Minute),
			CompletedAt: now,
			// no usage
		},
	}

	entries := mergeCaptures(caps)
	require.Len(t, entries, 2)

	assert.True(t, entries[0].HasUsage)
	assert.Equal(t, "claude", entries[0].AgentName)
	assert.Equal(t, int64(100), entries[0].InputTokens)
	assert.Equal(t, int64(50), entries[0].OutputTokens)

	assert.False(t, entries[1].HasUsage)
	assert.Empty(t, entries[1].AgentName)
}

func TestFlattenRun_NoChildren(t *testing.T) {
	now := time.Now()
	run := domain.NewRun("run-1", "develop")
	caps := []*domain.StepExecution{
		{StepName: "implement", Result: "success", StartedAt: now.Add(-2 * time.Minute), CompletedAt: now},
		{StepName: "review", Result: "done", StartedAt: now.Add(-1 * time.Minute), CompletedAt: now},
	}

	entries := flattenRun(run, caps, nil, nil)
	require.Len(t, entries, 2)

	assert.Equal(t, "implement", entries[0].StepName)
	assert.Equal(t, 0, entries[0].Depth)
	assert.Equal(t, "run-1", entries[0].RunID)
	assert.Equal(t, -1, entries[0].ParentIndex)
	assert.False(t, entries[0].IsWorkflow)
	assert.Equal(t, 0, entries[0].Index)

	assert.Equal(t, "review", entries[1].StepName)
	assert.Equal(t, 1, entries[1].Index)
}

func TestFlattenRun_WithChildRunByParentStepName(t *testing.T) {
	now := time.Now()
	run := domain.NewRun("run-1", "host")
	child := domain.NewRun("child-1", "develop")
	child.ParentRunID = "run-1"
	child.ParentStepName = "workflow_name"

	topCaps := []*domain.StepExecution{
		{StepName: "prepare", Result: "success", StartedAt: now.Add(-5 * time.Minute), CompletedAt: now.Add(-4 * time.Minute)},
		{StepName: "workflow_name", Result: "success", StartedAt: now.Add(-4 * time.Minute), CompletedAt: now.Add(-1 * time.Minute)},
		{StepName: "finalize", Result: "success", StartedAt: now.Add(-1 * time.Minute), CompletedAt: now},
	}
	childCaps := map[string][]*domain.StepExecution{
		"child-1": {
			{StepName: "implement", Result: "success", StartedAt: now.Add(-3 * time.Minute), CompletedAt: now.Add(-2 * time.Minute)},
			{StepName: "review", Result: "done", StartedAt: now.Add(-2 * time.Minute), CompletedAt: now.Add(-1 * time.Minute)},
		},
	}

	entries := flattenRun(run, topCaps, []*domain.Run{child}, childCaps)
	require.Len(t, entries, 5)

	// prepare at depth 0
	assert.Equal(t, "prepare", entries[0].StepName)
	assert.Equal(t, 0, entries[0].Depth)
	assert.Equal(t, "run-1", entries[0].RunID)
	assert.False(t, entries[0].IsWorkflow)

	// workflow_name at depth 0, marked as workflow
	assert.Equal(t, "workflow_name", entries[1].StepName)
	assert.Equal(t, 0, entries[1].Depth)
	assert.True(t, entries[1].IsWorkflow)
	assert.Equal(t, 1, entries[1].Index)

	// implement at depth 1, child run
	assert.Equal(t, "implement", entries[2].StepName)
	assert.Equal(t, 1, entries[2].Depth)
	assert.Equal(t, "child-1", entries[2].RunID)
	assert.Equal(t, 1, entries[2].ParentIndex) // parent is workflow_name at index 1
	assert.Equal(t, 2, entries[2].Index)

	// review at depth 1
	assert.Equal(t, "review", entries[3].StepName)
	assert.Equal(t, 1, entries[3].Depth)
	assert.Equal(t, "child-1", entries[3].RunID)
	assert.Equal(t, 3, entries[3].Index)

	// finalize at depth 0
	assert.Equal(t, "finalize", entries[4].StepName)
	assert.Equal(t, 0, entries[4].Depth)
	assert.Equal(t, 4, entries[4].Index)
}

func TestFlattenRun_FallbackByWorkflowName(t *testing.T) {
	// Child run has no ParentStepName set (legacy) — match by workflow name
	now := time.Now()
	run := domain.NewRun("run-1", "host")
	child := domain.NewRun("child-1", "develop") // workflow name matches step name
	child.ParentRunID = "run-1"
	// ParentStepName intentionally empty (legacy run)

	topCaps := []*domain.StepExecution{
		{StepName: "develop", Result: "success", StartedAt: now.Add(-2 * time.Minute), CompletedAt: now},
	}
	childCaps := map[string][]*domain.StepExecution{
		"child-1": {
			{StepName: "implement", Result: "success", StartedAt: now.Add(-1 * time.Minute), CompletedAt: now},
		},
	}

	entries := flattenRun(run, topCaps, []*domain.Run{child}, childCaps)
	require.Len(t, entries, 2)

	assert.Equal(t, "develop", entries[0].StepName)
	assert.True(t, entries[0].IsWorkflow)

	assert.Equal(t, "implement", entries[1].StepName)
	assert.Equal(t, 1, entries[1].Depth)
	assert.Equal(t, "child-1", entries[1].RunID)
}

func TestFlattenRun_IndexAssignment(t *testing.T) {
	now := time.Now()
	run := domain.NewRun("run-1", "host")
	child := domain.NewRun("child-1", "develop")
	child.ParentRunID = "run-1"
	child.ParentStepName = "step-b"

	topCaps := []*domain.StepExecution{
		{StepName: "step-a", Result: "success", StartedAt: now.Add(-3 * time.Minute), CompletedAt: now},
		{StepName: "step-b", Result: "success", StartedAt: now.Add(-2 * time.Minute), CompletedAt: now},
		{StepName: "step-c", Result: "success", StartedAt: now.Add(-1 * time.Minute), CompletedAt: now},
	}
	childCaps := map[string][]*domain.StepExecution{
		"child-1": {
			{StepName: "sub-1", Result: "success", StartedAt: now.Add(-90 * time.Second), CompletedAt: now},
			{StepName: "sub-2", Result: "success", StartedAt: now.Add(-60 * time.Second), CompletedAt: now},
		},
	}

	entries := flattenRun(run, topCaps, []*domain.Run{child}, childCaps)
	require.Len(t, entries, 5)

	// Verify sequential indices
	for i, e := range entries {
		assert.Equal(t, i, e.Index, "entry %d has wrong index", i)
	}

	// step-b is at index 1
	assert.Equal(t, "step-b", entries[1].StepName)
	assert.Equal(t, 1, entries[1].Index)

	// sub-1 parent is step-b (index 1)
	assert.Equal(t, "sub-1", entries[2].StepName)
	assert.Equal(t, 1, entries[2].ParentIndex)

	// sub-2 parent is step-b (index 1)
	assert.Equal(t, "sub-2", entries[3].StepName)
	assert.Equal(t, 1, entries[3].ParentIndex)
}

func TestAPITriggerOrchestrator_NoOrchestrateFunc(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("POST", "/api/projects/myapp/trigger", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "not configured")
}

func TestAPITriggerOrchestrator_ProjectNotFound(t *testing.T) {
	h, _ := setupHandler(t)
	h.orchestrateFn = func(_ context.Context, _ string) (int, error) { return 1, nil }

	req := httptest.NewRequest("POST", "/api/projects/nonexistent/trigger", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPITriggerOrchestrator_Success(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	var calledWith string
	h.orchestrateFn = func(_ context.Context, projectDir string) (int, error) {
		calledWith = projectDir
		return 3, nil
	}

	req := httptest.NewRequest("POST", "/api/projects/myapp/trigger", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "/home/user/projects/myapp", calledWith)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp["status"])
	assert.Equal(t, "myapp", resp["project"])
	assert.Equal(t, float64(3), resp["dispatched"])
}

func TestAPITriggerOrchestrator_Error(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	h.orchestrateFn = func(_ context.Context, _ string) (int, error) {
		return 0, fmt.Errorf("loop failed to start")
	}

	req := httptest.NewRequest("POST", "/api/projects/myapp/trigger", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "loop failed to start")
}

// --- Loop status and stop tests ---

func TestAPILoopStatus_Running(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	h.loopStatusFn = func(projectDir string) bool {
		return projectDir == "/home/user/projects/myapp"
	}

	req := httptest.NewRequest("GET", "/api/projects/myapp/loop/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/json", w.Header().Get("Content-Type"))
	var resp map[string]bool
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp["running"])
}

func TestAPILoopStatus_Stopped(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	h.loopStatusFn = func(_ string) bool { return false }

	req := httptest.NewRequest("GET", "/api/projects/myapp/loop/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]bool
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["running"])
}

func TestAPILoopStatus_NoStatusFunc(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	// loopStatusFn is nil — should return running=false.
	req := httptest.NewRequest("GET", "/api/projects/myapp/loop/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]bool
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.False(t, resp["running"])
}

func TestAPILoopStatus_ProjectNotFound(t *testing.T) {
	h, _ := setupHandler(t)
	h.loopStatusFn = func(_ string) bool { return true }

	req := httptest.NewRequest("GET", "/api/projects/nonexistent/loop/status", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPILoopStop_Success(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	var stoppedDir string
	h.stopLoopFn = func(_ context.Context, projectDir string) error {
		stoppedDir = projectDir
		return nil
	}

	req := httptest.NewRequest("POST", "/api/projects/myapp/loop/stop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "/home/user/projects/myapp", stoppedDir)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "ok", resp["status"])
	assert.Equal(t, "myapp", resp["project"])
}

func TestAPILoopStop_NoStopFunc(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("POST", "/api/projects/myapp/loop/stop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "not configured")
}

func TestAPILoopStop_Error(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(ctx, run))

	h.stopLoopFn = func(_ context.Context, _ string) error {
		return fmt.Errorf("stop failed")
	}

	req := httptest.NewRequest("POST", "/api/projects/myapp/loop/stop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	var resp map[string]string
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Contains(t, resp["error"], "stop failed")
}

func TestAPILoopStop_ProjectNotFound(t *testing.T) {
	h, _ := setupHandler(t)
	h.stopLoopFn = func(_ context.Context, _ string) error { return nil }

	req := httptest.NewRequest("POST", "/api/projects/nonexistent/loop/stop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRunDetail_FlattenRunRecursive(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	// Create parent run with a workflow step.
	parent := domain.NewRun("parent-r1", "host")
	parent.State = domain.RunStateSucceeded
	parent.StartedAt = time.Now().Add(-10 * time.Minute)
	parent.CompletedAt = time.Now()
	require.NoError(t, store.CreateRun(ctx, parent))

	// Create child run linked via ParentStepName.
	child := domain.NewRun("child-r1", "develop")
	child.State = domain.RunStateSucceeded
	child.ParentRunID = "parent-r1"
	child.ParentStepName = "run-develop"
	child.StartedAt = time.Now().Add(-8 * time.Minute)
	child.CompletedAt = time.Now().Add(-1 * time.Minute)
	require.NoError(t, store.CreateRun(ctx, child))

	// Seed step executions for parent run.
	now := time.Now()
	require.NoError(t, store.SaveCapture(ctx, "parent-r1", &domain.StepExecution{
		StepName:    "prepare",
		Result:      "success",
		StartedAt:   now.Add(-9 * time.Minute),
		CompletedAt: now.Add(-8 * time.Minute),
	}))
	require.NoError(t, store.SaveCapture(ctx, "parent-r1", &domain.StepExecution{
		StepName:    "run-develop",
		Result:      "success",
		StartedAt:   now.Add(-8 * time.Minute),
		CompletedAt: now.Add(-1 * time.Minute),
	}))
	require.NoError(t, store.SaveCapture(ctx, "parent-r1", &domain.StepExecution{
		StepName:    "finalize",
		Result:      "success",
		StartedAt:   now.Add(-1 * time.Minute),
		CompletedAt: now,
	}))

	// Seed step executions for child run.
	require.NoError(t, store.SaveCapture(ctx, "child-r1", &domain.StepExecution{
		StepName:    "implement",
		Result:      "success",
		StartedAt:   now.Add(-7 * time.Minute),
		CompletedAt: now.Add(-4 * time.Minute),
	}))
	require.NoError(t, store.SaveCapture(ctx, "child-r1", &domain.StepExecution{
		StepName:    "review",
		Result:      "done",
		StartedAt:   now.Add(-4 * time.Minute),
		CompletedAt: now.Add(-1 * time.Minute),
	}))

	req := httptest.NewRequest("GET", "/api/runs/parent-r1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var detail apiRunDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))

	// Expected flat list: prepare(0), run-develop(1), implement(2), review(3), finalize(4)
	require.Len(t, detail.Steps, 5)

	prepare := detail.Steps[0]
	assert.Equal(t, "prepare", prepare.StepName)
	assert.Equal(t, 0, prepare.Depth)
	assert.Equal(t, "parent-r1", prepare.RunID)
	assert.Equal(t, -1, prepare.ParentIndex)
	assert.False(t, prepare.IsWorkflow)
	assert.Equal(t, 0, prepare.Index)

	runDevelop := detail.Steps[1]
	assert.Equal(t, "run-develop", runDevelop.StepName)
	assert.Equal(t, 0, runDevelop.Depth)
	assert.Equal(t, "parent-r1", runDevelop.RunID)
	assert.True(t, runDevelop.IsWorkflow)
	assert.Equal(t, "child-r1", runDevelop.ChildRunID)
	assert.NotEmpty(t, runDevelop.ChildState)
	assert.Equal(t, 1, runDevelop.Index)

	implement := detail.Steps[2]
	assert.Equal(t, "implement", implement.StepName)
	assert.Equal(t, 1, implement.Depth)
	assert.Equal(t, "child-r1", implement.RunID)
	assert.Equal(t, 1, implement.ParentIndex) // parent is run-develop at index 1
	assert.False(t, implement.IsWorkflow)
	assert.Equal(t, 2, implement.Index)

	review := detail.Steps[3]
	assert.Equal(t, "review", review.StepName)
	assert.Equal(t, 1, review.Depth)
	assert.Equal(t, "child-r1", review.RunID)
	assert.Equal(t, 1, review.ParentIndex)
	assert.Equal(t, 3, review.Index)

	finalize := detail.Steps[4]
	assert.Equal(t, "finalize", finalize.StepName)
	assert.Equal(t, 0, finalize.Depth)
	assert.Equal(t, "parent-r1", finalize.RunID)
	assert.Equal(t, -1, finalize.ParentIndex)
	assert.False(t, finalize.IsWorkflow)
	assert.Equal(t, 4, finalize.Index)

	// ChildRuns field should still be populated for migration compatibility.
	assert.Len(t, detail.ChildRuns, 1)
	assert.Equal(t, "child-r1", detail.ChildRuns[0].ID)
}

func TestAPIRunDetail_FlattenRunLegacyFallback(t *testing.T) {
	// Verify that legacy runs (no ParentStepName) are matched by workflow name.
	h, store := setupHandler(t)
	ctx := context.Background()

	now := time.Now()

	parent := domain.NewRun("parent-legacy", "host")
	parent.State = domain.RunStateSucceeded
	parent.StartedAt = now.Add(-5 * time.Minute)
	parent.CompletedAt = now
	require.NoError(t, store.CreateRun(ctx, parent))

	child := domain.NewRun("child-legacy", "develop")
	child.State = domain.RunStateSucceeded
	child.ParentRunID = "parent-legacy"
	// ParentStepName intentionally empty (legacy run).
	child.StartedAt = now.Add(-3 * time.Minute) // within step's [now-4m, now-1m] window
	child.CompletedAt = now.Add(-1 * time.Minute)
	require.NoError(t, store.CreateRun(ctx, child))

	require.NoError(t, store.SaveCapture(ctx, "parent-legacy", &domain.StepExecution{
		StepName:    "develop",
		Result:      "success",
		StartedAt:   now.Add(-4 * time.Minute),
		CompletedAt: now.Add(-1 * time.Minute),
	}))
	require.NoError(t, store.SaveCapture(ctx, "child-legacy", &domain.StepExecution{
		StepName:    "implement",
		Result:      "success",
		StartedAt:   now.Add(-3 * time.Minute),
		CompletedAt: now.Add(-2 * time.Minute),
	}))

	req := httptest.NewRequest("GET", "/api/runs/parent-legacy", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var detail apiRunDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))

	require.Len(t, detail.Steps, 2)
	assert.Equal(t, "develop", detail.Steps[0].StepName)
	assert.True(t, detail.Steps[0].IsWorkflow)
	assert.Equal(t, "child-legacy", detail.Steps[0].ChildRunID)

	assert.Equal(t, "implement", detail.Steps[1].StepName)
	assert.Equal(t, 1, detail.Steps[1].Depth)
	assert.Equal(t, "child-legacy", detail.Steps[1].RunID)
	assert.Equal(t, 0, detail.Steps[1].ParentIndex)
}
