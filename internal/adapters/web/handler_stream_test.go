package web

import (
	"bufio"
	"context"
	"encoding/json"
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
