package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
)

func TestConsoleShell_RendersWithProjectSlugAndTaskID(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "cs-1", "develop", domain.RunStateSucceeded, "/home/user/myproj")

	req := httptest.NewRequest("GET", "/myproj/task-abc", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	body := w.Body.String()
	assert.Contains(t, body, `data-project-slug="myproj"`)
	assert.Contains(t, body, `data-task-id="task-abc"`)
	assert.Contains(t, body, `id="console-app"`)
	assert.Contains(t, body, `id="console-tabbar"`)
	assert.Contains(t, body, `id="console-stack"`)
	assert.Contains(t, body, `id="console-centre"`)
	assert.Contains(t, body, `id="console-footbar"`)
	assert.Contains(t, body, `/static/console.js`)
}

func TestConsoleShell_NoTaskID(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "cs-2", "develop", domain.RunStateSucceeded, "/home/user/myproj")

	req := httptest.NewRequest("GET", "/myproj", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()
	assert.Contains(t, body, `data-project-slug="myproj"`)
	assert.Contains(t, body, `data-task-id=""`)
}

func TestConsoleShell_UnknownProject_NotFound(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/nonexistent-project", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestConsoleStatic_ServesConsoleJS(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/static/console.js", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "console-project-tabs")
}

func TestLegacyRoot_RendersShellForMostRecentRunProject(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "lr-1", "develop", domain.RunStateSucceeded, "/home/user/zeta")
	seedRunWithProject(t, store, "lr-2", "develop", domain.RunStateSucceeded, "/home/user/alpha")

	// zeta's run is older alphabetically-first "alpha" would win a naive sort,
	// but zeta's run should win because it started more recently.
	run, err := store.GetRun(t.Context(), "lr-1")
	assert.NoError(t, err)
	run.StartedAt = time.Now().Add(time.Hour)
	assert.NoError(t, store.UpdateRun(t.Context(), run))

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Location"), "GET / should render the shell directly so client-side JS can still override the landing project via localStorage")
	assert.Contains(t, w.Body.String(), `data-project-slug="zeta"`)
}

func TestLegacyRoot_FallsBackToAlphabeticalWhenNoRunHasStarted(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "lr-3", "develop", domain.RunStatePending, "/home/user/zeta")
	seedRunWithProject(t, store, "lr-4", "develop", domain.RunStatePending, "/home/user/alpha")

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `data-project-slug="alpha"`)
}

func TestLegacyRoot_NoProjects_RendersEmptyShell(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), `id="console-app"`)
}

func TestLegacyProjectRedirect(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "lp-1", "develop", domain.RunStateSucceeded, "/home/user/myproj")

	req := httptest.NewRequest("GET", "/projects/myproj", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/myproj", w.Header().Get("Location"))

	req = httptest.NewRequest("GET", "/projects/myproj/runs", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/myproj", w.Header().Get("Location"))
}

func TestLegacyProjectRedirect_NotFound(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/projects/nonexistent", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestLegacyRunsRedirect(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/runs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/", w.Header().Get("Location"))
}

func TestLegacyRunRedirect_WithTaskID(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "run-with-task", "develop", domain.RunStateSucceeded, "/home/user/myproj")
	run, err := store.GetRun(t.Context(), "run-with-task")
	assert.NoError(t, err)
	run.TaskID = "task-xyz"
	assert.NoError(t, store.UpdateRun(t.Context(), run))

	req := httptest.NewRequest("GET", "/runs/run-with-task", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/myproj/task-xyz", w.Header().Get("Location"))
}

func TestLegacyRunRedirect_WithoutTaskID(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "run-no-task", "develop", domain.RunStateSucceeded, "/home/user/myproj")

	req := httptest.NewRequest("GET", "/runs/run-no-task", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/myproj", w.Header().Get("Location"))
}

func TestLegacyFailedTasksRedirect(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/failed-tasks", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/", w.Header().Get("Location"))
}
