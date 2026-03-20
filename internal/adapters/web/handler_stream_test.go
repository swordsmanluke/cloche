package web

import (
	"bufio"
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
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupHandlerWithBroadcaster(t *testing.T) (*Handler, *sqlite.Store, *logstream.Broadcaster) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	b := logstream.NewBroadcaster()
	h, err := NewHandler(store, store, WithLogBroadcaster(b))
	require.NoError(t, err)
	return h, store, b
}

func TestSSE_CompletedRun_ServesFullLog(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)

	dir := t.TempDir()

	// Create a completed run
	ctx := context.Background()
	run := domain.NewRun("sse-test-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write full.log
	outputDir := filepath.Join(dir, ".cloche", "sse-test-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	logContent := "[2026-03-03T10:15:00Z] [status] step_started: build\n" +
		"[2026-03-03T10:15:01Z] [script] npm run build\n" +
		"[2026-03-03T10:15:02Z] [llm] Claude: Starting analysis\n"
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"), []byte(logContent), 0644))

	req := httptest.NewRequest("GET", "/api/runs/sse-test-1/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "text/event-stream", w.Header().Get("Content-Type"))

	// Parse SSE events
	body := w.Body.String()
	lines := strings.Split(body, "\n")

	var events []logstream.LogLine
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var ll logstream.LogLine
			if json.Unmarshal([]byte(data), &ll) == nil {
				events = append(events, ll)
			}
		}
	}

	require.Len(t, events, 3)
	assert.Equal(t, "status", events[0].Type)
	assert.Equal(t, "step_started: build", events[0].Content)
	assert.Equal(t, "script", events[1].Type)
	assert.Equal(t, "npm run build", events[1].Content)
	assert.Equal(t, "llm", events[2].Type)
	assert.Equal(t, "Claude: Starting analysis", events[2].Content)

	// Should contain done event
	assert.Contains(t, body, "event: done")
}

func TestSSE_ActiveRun_StreamsLive(t *testing.T) {
	h, store, b := setupHandlerWithBroadcaster(t)

	// Create a running run
	ctx := context.Background()
	run := domain.NewRun("sse-live-1", "develop")
	run.ProjectDir = t.TempDir()
	run.Start()
	run.ContainerID = "abc123"
	require.NoError(t, store.CreateRun(ctx, run))

	// Start a subscriber to simulate an active broadcast
	b.Subscribe("sse-live-1")

	// Use a real HTTP server for streaming
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Publish some lines in a goroutine
	go func() {
		time.Sleep(50 * time.Millisecond)
		b.Publish("sse-live-1", logstream.LogLine{
			Timestamp: "2026-03-03T10:15:00Z",
			Type:      "status",
			Content:   "step_started: build",
		})
		time.Sleep(50 * time.Millisecond)
		b.Publish("sse-live-1", logstream.LogLine{
			Timestamp: "2026-03-03T10:15:01Z",
			Type:      "script",
			Content:   "compiling...",
		})
		time.Sleep(50 * time.Millisecond)
		b.Finish("sse-live-1")
	}()

	resp, err := http.Get(srv.URL + "/api/runs/sse-live-1/stream")
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/event-stream", resp.Header.Get("Content-Type"))

	var events []logstream.LogLine
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var ll logstream.LogLine
			if json.Unmarshal([]byte(data), &ll) == nil {
				events = append(events, ll)
			}
		}
		if strings.HasPrefix(line, "event: done") {
			break
		}
	}

	require.Len(t, events, 2)
	assert.Equal(t, "status", events[0].Type)
	assert.Equal(t, "step_started: build", events[0].Content)
	assert.Equal(t, "script", events[1].Type)
	assert.Equal(t, "compiling...", events[1].Content)
}

func TestSSE_RunNotFound(t *testing.T) {
	h, _, _ := setupHandlerWithBroadcaster(t)

	req := httptest.NewRequest("GET", "/api/runs/nonexistent/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestSSE_NoBroadcaster_ActiveRun(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Handler without broadcaster
	h, err := NewHandler(store, store)
	require.NoError(t, err)

	ctx := context.Background()
	run := domain.NewRun("no-bc-1", "develop")
	run.Start()
	run.ContainerID = "abc123"
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/runs/no-bc-1/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestParseFullLogLine(t *testing.T) {
	tests := []struct {
		input    string
		expected logstream.LogLine
	}{
		{
			input: "[2026-03-03T10:15:00Z] [status] step_started: build",
			expected: logstream.LogLine{
				Timestamp: "2026-03-03T10:15:00Z",
				Type:      "status",
				Content:   "step_started: build",
			},
		},
		{
			input: "[2026-03-03T10:15:01Z] [script] npm run build",
			expected: logstream.LogLine{
				Timestamp: "2026-03-03T10:15:01Z",
				Type:      "script",
				Content:   "npm run build",
			},
		},
		{
			input: "[2026-03-03T10:15:02Z] [llm] Claude: Reading files...",
			expected: logstream.LogLine{
				Timestamp: "2026-03-03T10:15:02Z",
				Type:      "llm",
				Content:   "Claude: Reading files...",
			},
		},
		{
			input: "unstructured line",
			expected: logstream.LogLine{
				Type:    "script",
				Content: "unstructured line",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := parseFullLogLine(tt.input)
			assert.Equal(t, tt.expected.Type, result.Type)
			assert.Equal(t, tt.expected.Content, result.Content)
			if tt.expected.Timestamp != "" {
				assert.Equal(t, tt.expected.Timestamp, result.Timestamp)
			}
		})
	}
}

func TestSSE_CompletedRun_LargeJSONLines(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)

	dir := t.TempDir()

	ctx := context.Background()
	run := domain.NewRun("sse-large-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Build a large assistant event (>64KB) to simulate real Claude Code output.
	// Default bufio.Scanner buffer is 64KB; this verifies we handle larger lines.
	largeText := strings.Repeat("x", 100*1024) // 100KB of text
	assistantJSON := `{"type":"assistant","message":{"content":[{"type":"text","text":"` + largeText + `"}]}}`

	outputDir := filepath.Join(dir, ".cloche", "sse-large-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	logContent := "[2026-03-03T10:15:00Z] [status] step_started: build\n" +
		"[2026-03-03T10:15:01Z] [llm] " + assistantJSON + "\n" +
		"[2026-03-03T10:15:02Z] [status] step_completed: build -> success\n"
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"), []byte(logContent), 0644))

	req := httptest.NewRequest("GET", "/api/runs/sse-large-1/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

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

	// All three lines should be present: status, llm (parsed from large JSON), status
	require.Len(t, events, 3, "expected all log lines including those after large JSON line")
	assert.Equal(t, "status", events[0].Type)
	assert.Equal(t, "step_started: build", events[0].Content)
	assert.Equal(t, "llm", events[1].Type)
	assert.Contains(t, events[1].Content, largeText)
	assert.Equal(t, "status", events[2].Type)
	assert.Equal(t, "step_completed: build -> success", events[2].Content)

	assert.Contains(t, body, "event: done")
}

func TestSSE_CompletedRun_NoFullLog(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)

	// Create a completed run with no full.log
	ctx := context.Background()
	run := domain.NewRun("sse-nolog-1", "develop")
	run.ProjectDir = t.TempDir()
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/runs/sse-nolog-1/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "event: done")
}

// writeNLogLines creates a full.log with n visible log lines.
func writeNLogLines(t *testing.T, dir, runID string, n int) {
	t.Helper()
	outputDir := filepath.Join(dir, ".cloche", runID, "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))

	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "[2026-03-03T10:15:%02dZ] [script] line %d\n", i%60, i)
	}
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"), []byte(b.String()), 0644))
}

func TestSSE_CompletedRun_TailsLast1000(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)
	dir := t.TempDir()

	ctx := context.Background()
	run := domain.NewRun("sse-tail-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write 1500 log lines
	writeNLogLines(t, dir, "sse-tail-1", 1500)

	req := httptest.NewRequest("GET", "/api/runs/sse-tail-1/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Parse events
	var events []logstream.LogLine
	var metaEvent map[string]int
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			// Try as meta event (follows "event: meta")
			var meta map[string]int
			if json.Unmarshal([]byte(data), &meta) == nil {
				if _, ok := meta["total_lines"]; ok {
					metaEvent = meta
					continue
				}
			}
			var ll logstream.LogLine
			if json.Unmarshal([]byte(data), &ll) == nil {
				events = append(events, ll)
			}
		}
	}

	// Should have meta event indicating skipped lines
	require.NotNil(t, metaEvent, "expected meta SSE event")
	assert.Equal(t, 1500, metaEvent["total_lines"])
	assert.Equal(t, 500, metaEvent["skipped"])

	// Should only have 1000 log events (the last 1000 of 1500)
	require.Len(t, events, 1000)
	// First event should be line 500 (0-indexed)
	assert.Equal(t, "line 500", events[0].Content)
	// Last event should be line 1499
	assert.Equal(t, "line 1499", events[999].Content)
}

func TestSSE_CompletedRun_NoMetaWhenUnderLimit(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)
	dir := t.TempDir()

	ctx := context.Background()
	run := domain.NewRun("sse-small-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write only 50 log lines (under the 1000 limit)
	writeNLogLines(t, dir, "sse-small-1", 50)

	req := httptest.NewRequest("GET", "/api/runs/sse-small-1/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Should NOT contain meta event
	assert.NotContains(t, body, "event: meta")

	// Should have all 50 lines
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
	assert.Len(t, events, 50)
}

func TestAPILogs_Pagination(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)
	dir := t.TempDir()

	ctx := context.Background()
	run := domain.NewRun("logs-page-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write 2500 log lines
	writeNLogLines(t, dir, "logs-page-1", 2500)

	// Request last 1000 lines (default)
	req := httptest.NewRequest("GET", "/api/runs/logs-page-1/logs", nil)
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

	assert.Equal(t, 2500, resp.Total)
	assert.Equal(t, 1500, resp.Start)
	assert.Equal(t, 2500, resp.End)
	assert.Len(t, resp.Lines, 1000)
	assert.Equal(t, "line 1500", resp.Lines[0].Content)
	assert.Equal(t, "line 2499", resp.Lines[999].Content)
}

func TestAPILogs_PaginateEarlier(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)
	dir := t.TempDir()

	ctx := context.Background()
	run := domain.NewRun("logs-page-2", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write 2500 log lines
	writeNLogLines(t, dir, "logs-page-2", 2500)

	// Request earlier page: lines before index 1500, limit 1000
	req := httptest.NewRequest("GET", "/api/runs/logs-page-2/logs?end=1500&limit=1000", nil)
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

	assert.Equal(t, 2500, resp.Total)
	assert.Equal(t, 500, resp.Start)
	assert.Equal(t, 1500, resp.End)
	assert.Len(t, resp.Lines, 1000)
	assert.Equal(t, "line 500", resp.Lines[0].Content)
	assert.Equal(t, "line 1499", resp.Lines[999].Content)
}

func TestAPILogs_FirstPage(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)
	dir := t.TempDir()

	ctx := context.Background()
	run := domain.NewRun("logs-page-3", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// Write 2500 log lines
	writeNLogLines(t, dir, "logs-page-3", 2500)

	// Request the very first page: end=500, limit=1000 — should clamp start to 0
	req := httptest.NewRequest("GET", "/api/runs/logs-page-3/logs?end=500&limit=1000", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp struct {
		Lines []logstream.LogLine `json:"lines"`
		Total int                 `json:"total"`
		Start int                 `json:"start"`
		End   int                 `json:"end"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))

	assert.Equal(t, 2500, resp.Total)
	assert.Equal(t, 0, resp.Start)
	assert.Equal(t, 500, resp.End)
	assert.Len(t, resp.Lines, 500)
	assert.Equal(t, "line 0", resp.Lines[0].Content)
}

func TestSSE_CompletedRun_StepNamesInferredFromFullLog(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)

	dir := t.TempDir()
	ctx := context.Background()
	run := domain.NewRun("sse-stepnames-1", "develop")
	run.ProjectDir = dir
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	// full.log with two sequential steps
	outputDir := filepath.Join(dir, ".cloche", "sse-stepnames-1", "output")
	require.NoError(t, os.MkdirAll(outputDir, 0755))
	logContent := "[2026-03-03T10:15:00Z] [status] step_started: build\n" +
		"[2026-03-03T10:15:01Z] [script] npm run build\n" +
		"[2026-03-03T10:15:02Z] [llm] Claude: compiling\n" +
		"[2026-03-03T10:15:03Z] [status] step_completed: build -> success\n" +
		"[2026-03-03T10:15:04Z] [status] step_started: test\n" +
		"[2026-03-03T10:15:05Z] [script] npm test\n" +
		"[2026-03-03T10:15:06Z] [status] step_completed: test -> success\n"
	require.NoError(t, os.WriteFile(filepath.Join(outputDir, "full.log"), []byte(logContent), 0644))

	req := httptest.NewRequest("GET", "/api/runs/sse-stepnames-1/stream", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var events []logstream.LogLine
	for _, line := range strings.Split(w.Body.String(), "\n") {
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			var ll logstream.LogLine
			if json.Unmarshal([]byte(data), &ll) == nil {
				events = append(events, ll)
			}
		}
	}

	require.Len(t, events, 7)
	// step_started: build — step name is "build"
	assert.Equal(t, "build", events[0].StepName)
	// script line during build
	assert.Equal(t, "build", events[1].StepName)
	// llm line during build
	assert.Equal(t, "build", events[2].StepName)
	// step_completed: build — step name is "build"
	assert.Equal(t, "build", events[3].StepName)
	// step_started: test
	assert.Equal(t, "test", events[4].StepName)
	// script line during test
	assert.Equal(t, "test", events[5].StepName)
	// step_completed: test
	assert.Equal(t, "test", events[6].StepName)
}

func TestReadVisibleLogLines_StepNameInference(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "full.log")

	logContent := "[2026-03-03T10:15:00Z] [status] step_started: implement\n" +
		"[2026-03-03T10:15:01Z] [llm] Reading files...\n" +
		"[2026-03-03T10:15:02Z] [script] bash -c 'echo hello'\n" +
		"[2026-03-03T10:15:03Z] [status] step_completed: implement -> success\n" +
		"[2026-03-03T10:15:04Z] [status] step_started: review\n" +
		"[2026-03-03T10:15:05Z] [script] review output\n" +
		"[2026-03-03T10:15:06Z] [status] step_completed: review -> fail\n"
	require.NoError(t, os.WriteFile(logPath, []byte(logContent), 0644))

	lines, err := readVisibleLogLines(logPath)
	require.NoError(t, err)
	require.Len(t, lines, 7)

	assert.Equal(t, "implement", lines[0].StepName) // step_started: implement
	assert.Equal(t, "implement", lines[1].StepName) // llm line
	assert.Equal(t, "implement", lines[2].StepName) // script line
	assert.Equal(t, "implement", lines[3].StepName) // step_completed: implement
	assert.Equal(t, "review", lines[4].StepName)    // step_started: review
	assert.Equal(t, "review", lines[5].StepName)    // script line
	assert.Equal(t, "review", lines[6].StepName)    // step_completed: review

	// Verify content is preserved correctly
	assert.Equal(t, "step_started: implement", lines[0].Content)
	assert.Equal(t, "step_completed: implement -> success", lines[3].Content)
	assert.Equal(t, "step_started: review", lines[4].Content)
	assert.Equal(t, "step_completed: review -> fail", lines[6].Content)
}

func TestReadVisibleLogLines_NoStepContext(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "full.log")

	// Lines without step_started context should have empty StepName
	logContent := "[2026-03-03T10:15:00Z] [status] run started\n" +
		"[2026-03-03T10:15:01Z] [script] some output\n"
	require.NoError(t, os.WriteFile(logPath, []byte(logContent), 0644))

	lines, err := readVisibleLogLines(logPath)
	require.NoError(t, err)
	require.Len(t, lines, 2)

	assert.Equal(t, "", lines[0].StepName)
	assert.Equal(t, "", lines[1].StepName)
}

func TestAPILogs_RunNotFound(t *testing.T) {
	h, _, _ := setupHandlerWithBroadcaster(t)

	req := httptest.NewRequest("GET", "/api/runs/nonexistent/logs", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPILogs_NoLogFile(t *testing.T) {
	h, store, _ := setupHandlerWithBroadcaster(t)

	ctx := context.Background()
	run := domain.NewRun("logs-nolog-1", "develop")
	run.ProjectDir = t.TempDir()
	run.Start()
	run.ContainerID = "abc123"
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/runs/logs-nolog-1/logs", nil)
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
