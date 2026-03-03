package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
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
	removed    []string
}

func newMockContainerManager() *mockContainerManager {
	return &mockContainerManager{containers: map[string]bool{}}
}

func (m *mockContainerManager) Logs(_ context.Context, containerID string) (string, error) {
	return "mock logs", nil
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
	return &ports.ContainerStatus{Running: false, ExitCode: 0}, nil
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
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "run-a1")
	assert.Contains(t, body, "run-b1")

	// With project filter: only matching runs
	req = httptest.NewRequest("GET", "/?project=/home/user/alpha", nil)
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

	req := httptest.NewRequest("GET", "/", nil)
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

func TestRunDetail_ContainerAvailable(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-c1", "develop", "/proj", "cid-1234567890ab", true)

	req := httptest.NewRequest("GET", "/runs/run-c1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Container available")
	assert.Contains(t, body, "delete-container-btn")
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

	req := httptest.NewRequest("GET", "/runs/run-c2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, "Container removed")
	assert.NotContains(t, body, `id="delete-container-btn"`)
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

func TestRunsList_ContainerCount(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedRunWithContainer(t, store, mgr, "run-cc1", "develop", "/home/user/alpha", "cid-1", true)
	seedRunWithContainer(t, store, mgr, "run-cc2", "develop", "/home/user/alpha", "cid-2", true)
	seedRunWithContainer(t, store, mgr, "run-cc3", "deploy", "/home/user/beta", "cid-3", false)

	req := httptest.NewRequest("GET", "/", nil)
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
	seedRunWithProject(t, store, "p3", "develop", domain.RunStateRunning, "/home/user/alpha")

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

	dirs := map[string]string{}
	for _, p := range projects {
		dirs[p.Dir] = p.Label
	}
	assert.Equal(t, "alpha", dirs["/home/user/alpha"])
	assert.Equal(t, "beta", dirs["/home/user/beta"])
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
