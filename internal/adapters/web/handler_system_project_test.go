package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
)

// TestSystemProjectTab covers the "system" pseudo-project (see
// SystemProjectSlug): user-initiated tasks/runs created outside a
// registered project (ProjectDir "") must still surface in the console —
// the tab bar, the task stack, and the run detail view — rather than being
// silently invisible.
func TestSystemProjectTab(t *testing.T) {
	h, store := setupHandler(t)
	h.taskStore = store
	ctx := context.Background()

	task := &domain.Task{
		ID:         "user-6ve8",
		Title:      "develop (user-initiated)",
		Source:     domain.TaskSourceUserInitiated,
		ProjectDir: "",
		CreatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveTask(ctx, task))
	attempt := &domain.Attempt{
		ID:        "att-sys1",
		TaskID:    "user-6ve8",
		StartedAt: time.Now().Add(-time.Minute),
		Result:    domain.AttemptResultRunning,
	}
	require.NoError(t, store.SaveAttempt(ctx, attempt))

	run := domain.NewRun("run-sys1", "develop")
	run.ProjectDir = ""
	run.TaskID = "user-6ve8"
	run.AttemptID = "att-sys1"
	run.State = domain.RunStateRunning
	run.StartedAt = attempt.StartedAt
	run.ActiveSteps = []string{"build"}
	require.NoError(t, store.CreateRun(ctx, run))

	t.Run("tab appears in the projects summary", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		var projects []struct {
			Dir         string `json:"dir"`
			Slug        string `json:"slug"`
			Label       string `json:"label"`
			LoopRunning bool   `json:"loop_running"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &projects))

		var found *struct {
			Dir         string `json:"dir"`
			Slug        string `json:"slug"`
			Label       string `json:"label"`
			LoopRunning bool   `json:"loop_running"`
		}
		for i := range projects {
			if projects[i].Slug == SystemProjectSlug {
				found = &projects[i]
			}
		}
		require.NotNil(t, found, "expected a %q tab in the projects summary", SystemProjectSlug)
		assert.Equal(t, "", found.Dir)
		assert.Equal(t, SystemProjectLabel, found.Label)
		assert.False(t, found.LoopRunning)
	})

	t.Run("resolveProjectDir does not 404 on the system slug", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/system", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("task stack lists the task", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects/system/tasks/stack", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		var stack TaskStack
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &stack))
		require.Len(t, stack.Running, 1)
		assert.Equal(t, "user-6ve8", stack.Running[0].TaskID)
		assert.Equal(t, "run-sys1", stack.Running[0].RunID)
		assert.Equal(t, "develop (user-initiated)", stack.Running[0].Title)
	})

	t.Run("run detail loads", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/runs/run-sys1", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		var detail apiRunDetail
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
		assert.Equal(t, "run-sys1", detail.ID)
		assert.Equal(t, "", detail.ProjectDir)
		assert.Equal(t, "user-6ve8", detail.TaskID)
	})

	t.Run("loop status reports not running, no orchestration loop", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/projects/system/loop/status", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code)

		var body struct {
			Running bool `json:"running"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		assert.False(t, body.Running)
	})
}

// TestSystemProjectTab_AbsentWithoutProjectlessRows confirms the system tab
// stays hidden — not just empty — when no task or run has an empty
// ProjectDir, so a fresh install with only registered projects sees no
// extra tab.
func TestSystemProjectTab_AbsentWithoutProjectlessRows(t *testing.T) {
	h, store := setupHandler(t)
	seedRunWithProject(t, store, "p1", "develop", domain.RunStateSucceeded, "/home/user/alpha")

	req := httptest.NewRequest("GET", "/api/projects", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var projects []struct {
		Slug string `json:"slug"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &projects))
	for _, p := range projects {
		assert.NotEqual(t, SystemProjectSlug, p.Slug)
	}
}
