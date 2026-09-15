package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
)

func seedTaskRunWithContainer(t *testing.T, store interface {
	CreateRun(ctx context.Context, run *domain.Run) error
}, mgr *mockContainerManager, id, workflow, projectDir, containerID, taskID string, kept bool, sizeBytes int64) {
	t.Helper()
	ctx := context.Background()
	run := domain.NewRun(id, workflow)
	run.ProjectDir = projectDir
	run.TaskID = taskID
	run.Start()
	run.ContainerID = containerID
	run.ContainerKept = kept
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))
	if kept {
		mgr.containers[containerID] = true
		mgr.sizes[containerID] = sizeBytes
	}
}

func TestAPIProjectContainers_GroupsByTask(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)

	seedTaskRunWithContainer(t, store, mgr, "run-c1", "develop", "/home/user/alpha", "cid-c1", "task-1", true, 1024*1024)
	seedTaskRunWithContainer(t, store, mgr, "run-c2", "finalize", "/home/user/alpha", "cid-c2", "task-1", true, 2048)
	seedTaskRunWithContainer(t, store, mgr, "run-c3", "develop", "/home/user/alpha", "cid-c3", "task-2", true, 4096)
	// Not kept — should be excluded entirely.
	seedTaskRunWithContainer(t, store, mgr, "run-c4", "develop", "/home/user/alpha", "cid-c4", "task-2", false, 0)
	// Different project — should not appear.
	seedTaskRunWithContainer(t, store, mgr, "run-c5", "develop", "/home/user/beta", "cid-c5", "task-3", true, 0)

	req := httptest.NewRequest("GET", "/api/projects/alpha/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var groups []apiContainerTaskGroup
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &groups))
	require.Len(t, groups, 2)

	byTask := map[string]apiContainerTaskGroup{}
	for _, g := range groups {
		byTask[g.TaskID] = g
	}

	task1 := byTask["task-1"]
	require.Len(t, task1.Containers, 2)
	var ids []string
	for _, c := range task1.Containers {
		ids = append(ids, c.ContainerID)
		assert.Equal(t, "available", c.State)
	}
	assert.ElementsMatch(t, []string{"cid-c1", "cid-c2"}, ids)

	task2 := byTask["task-2"]
	require.Len(t, task2.Containers, 1)
	assert.Equal(t, "cid-c3", task2.Containers[0].ContainerID)
	assert.Equal(t, int64(4096), task2.Containers[0].SizeBytes)
}

func TestAPIProjectContainers_NoneKept(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	seedTaskRunWithContainer(t, store, mgr, "run-c6", "develop", "/home/user/gamma", "cid-c6", "task-4", false, 0)

	req := httptest.NewRequest("GET", "/api/projects/gamma/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var groups []apiContainerTaskGroup
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &groups))
	assert.Empty(t, groups)
}

func TestAPIProjectContainers_ProjectNotFound(t *testing.T) {
	h, _, _ := setupHandlerWithContainerManager(t)

	req := httptest.NewRequest("GET", "/api/projects/nonexistent/containers", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
