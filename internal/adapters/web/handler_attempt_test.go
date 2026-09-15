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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/logstream"
)

// writeV2FullLog writes a full.log at the v2 path:
// <dir>/.cloche/logs/<taskID>/<attemptID>/full.log
func writeV2FullLog(t *testing.T, dir, taskID, attemptID, content string) {
	t.Helper()
	logDir := filepath.Join(dir, ".cloche", "logs", taskID, attemptID)
	require.NoError(t, os.MkdirAll(logDir, 0755))
	require.NoError(t, os.WriteFile(filepath.Join(logDir, "full.log"), []byte(content), 0644))
}

func TestAttemptStream_NotFound(t *testing.T) {
	h, _, _ := setupHandlerWithBroadcaster(t)

	req := httptest.NewRequest("GET", "/api/attempts/xxxx/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAttemptStream_CompletedRun_V2LogPath(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	h, err := NewHandler(store, store, WithLogBroadcaster(logstream.NewBroadcaster()))
	require.NoError(t, err)

	dir := t.TempDir()
	ctx := context.Background()

	run := domain.NewRun("main-ab12-implement", "main")
	run.ProjectDir = dir
	run.TaskID = "task-001"
	run.AttemptID = "ab12"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	logContent := "[2026-03-03T10:15:00Z] [status] step_started: implement\n" +
		"[2026-03-03T10:15:01Z] [script] building...\n"
	writeV2FullLog(t, dir, "task-001", "ab12", logContent)

	req := httptest.NewRequest("GET", "/api/attempts/ab12/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))

	body := w.Body.String()
	var events []logstream.LogLine
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var ll logstream.LogLine
			if json.Unmarshal([]byte(data), &ll) == nil {
				events = append(events, ll)
			}
		}
	}
	require.Len(t, events, 2)
	assert.Equal(t, "status", events[0].Type)
	assert.Equal(t, "step_started: implement", events[0].Content)
	assert.Equal(t, "script", events[1].Type)
	assert.Contains(t, body, "event: done")
}

func TestAttemptLogs_NotFound(t *testing.T) {
	h, _, _ := setupHandlerWithBroadcaster(t)

	req := httptest.NewRequest("GET", "/api/attempts/xxxx/logs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAttemptLogs_V2LogPath(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	h, err := NewHandler(store, store)
	require.NoError(t, err)

	dir := t.TempDir()
	ctx := context.Background()

	run := domain.NewRun("main-cd34-implement", "main")
	run.ProjectDir = dir
	run.TaskID = "task-002"
	run.AttemptID = "cd34"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write 50 log lines at v2 path
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&sb, "[2026-03-03T10:15:%02dZ] [script] line %d\n", i%60, i)
	}
	writeV2FullLog(t, dir, "task-002", "cd34", sb.String())

	req := httptest.NewRequest("GET", "/api/attempts/cd34/logs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Lines []logstream.LogLine `json:"lines"`
		Total int                 `json:"total"`
		Start int                 `json:"start"`
		End   int                 `json:"end"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 50, resp.Total)
	assert.Len(t, resp.Lines, 50)
	assert.Equal(t, "line 0", resp.Lines[0].Content)
}

func TestAttemptLogs_NoLogFile(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	h, err := NewHandler(store, store)
	require.NoError(t, err)

	ctx := context.Background()
	run := domain.NewRun("main-ef56-implement", "main")
	run.ProjectDir = t.TempDir()
	run.TaskID = "task-003"
	run.AttemptID = "ef56"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/attempts/ef56/logs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Lines []logstream.LogLine `json:"lines"`
		Total int                 `json:"total"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, 0, resp.Total)
	assert.Empty(t, resp.Lines)
}

func TestTaskDetail_NotFound(t *testing.T) {
	h, _ := setupHandler(t)

	req := httptest.NewRequest("GET", "/tasks/nonexistent-task", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// TestTaskDetail_RedirectsToConsoleShell verifies the legacy /tasks/{id}
// route resolves the task's project and redirects into the new
// /{slug}/{task-id} console shell scheme rather than rendering a page.
func TestTaskDetail_RedirectsToConsoleShell(t *testing.T) {
	h, store := setupHandler(t)

	ctx := context.Background()
	run1 := domain.NewRun("main-aa11-implement", "main")
	run1.TaskID = "task-det-1"
	run1.TaskTitle = "My Task"
	run1.AttemptID = "aa11"
	run1.ProjectDir = "/home/user/myproj"
	run1.Start()
	run1.Complete(domain.RunStateFailed)
	require.NoError(t, store.CreateRun(ctx, run1))

	req := httptest.NewRequest("GET", "/tasks/task-det-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusFound, w.Code)
	assert.Equal(t, "/myproj/task-det-1", w.Header().Get("Location"))
}

func TestFullLogPath_V2(t *testing.T) {
	run := &domain.Run{
		ID:         "main-ab12-implement",
		ProjectDir: "/project",
		TaskID:     "task-001",
		AttemptID:  "ab12",
	}
	path := fullLogPath(run)
	assert.Equal(t, "/project/.cloche/logs/task-001/ab12/full.log", path)
}

func TestFullLogPath_Legacy(t *testing.T) {
	run := &domain.Run{
		ID:         "main-bold-fox",
		ProjectDir: "/project",
	}
	path := fullLogPath(run)
	assert.Equal(t, "/project/.cloche/main-bold-fox/output/full.log", path)
}
