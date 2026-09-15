package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func seedParkedRun(t *testing.T, store *sqlite.Store, id, threadID string) {
	t.Helper()
	ctx := context.Background()
	run := domain.NewRun(id, "develop")
	run.ProjectDir = "/proj"
	run.TaskID = "task-" + id
	run.Start()
	run.State = domain.RunStateParked
	run.ParkedThreadID = threadID
	run.ParkedTitle = "Which schema should I use?"
	require.NoError(t, store.CreateRun(ctx, run))

	require.NoError(t, store.SaveCapture(ctx, id, &domain.StepExecution{
		StepName:  "implement",
		StartedAt: time.Now().Add(-time.Hour),
	}))
	require.NoError(t, store.SaveCapture(ctx, id, &domain.StepExecution{
		StepName:    "implement",
		Result:      domain.StepParked,
		CompletedAt: time.Now().Add(-90 * time.Second),
	}))
}

func TestAPIRunDetail_Parked(t *testing.T) {
	h, store := setupHandler(t)
	seedParkedRun(t, store, "parked-run-1", "thread-abc")

	req := httptest.NewRequest("GET", "/api/runs/parked-run-1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var detail apiRunDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &detail))
	assert.Equal(t, "parked", detail.State)
	assert.Equal(t, "Which schema should I use?", detail.ParkedTitle)
	assert.GreaterOrEqual(t, detail.ParkedSeconds, int64(85))

	require.Len(t, detail.Steps, 1)
	assert.Equal(t, "parked", detail.Steps[0].Result)
}

func TestAPIRunThread_NotParked(t *testing.T) {
	h, store := setupHandler(t)
	seedRun(t, store, "not-parked", "develop", domain.RunStateRunning)

	req := httptest.NewRequest("GET", "/api/runs/not-parked/thread", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRunThread_NoThreadService(t *testing.T) {
	h, store := setupHandler(t) // no WithGetThreadFunc configured
	seedParkedRun(t, store, "parked-run-2", "thread-xyz")

	req := httptest.NewRequest("GET", "/api/runs/parked-run-2/thread", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestAPIRunThread_Success(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	var gotAddress string
	h, err := NewHandler(store, store, WithGetThreadFunc(func(ctx context.Context, address string) (ThreadSummary, []ThreadMessage, error) {
		gotAddress = address
		return ThreadSummary{
				Address: "cloche/schema-1",
				Title:   "Which schema should I use?",
				State:   "awaiting_user",
			}, []ThreadMessage{
				{Author: "agent", Body: "Which schema should I use?", CreatedAt: "2026-01-01T00:00:00Z"},
			}, nil
	}))
	require.NoError(t, err)

	seedParkedRun(t, store, "parked-run-3", "thread-def")

	req := httptest.NewRequest("GET", "/api/runs/parked-run-3/thread", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "thread-def", gotAddress)

	var resp apiThreadDetail
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "cloche/schema-1", resp.Address)
	require.Len(t, resp.Messages, 1)
	assert.Equal(t, "agent", resp.Messages[0].Author)
}

func TestAPIRunThreadReply_Success(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	var gotAddress, gotBody string
	h, err := NewHandler(store, store, WithReplyThreadFunc(func(ctx context.Context, address, body string) error {
		gotAddress, gotBody = address, body
		return nil
	}))
	require.NoError(t, err)

	seedParkedRun(t, store, "parked-run-4", "thread-ghi")

	req := httptest.NewRequest("POST", "/api/runs/parked-run-4/thread/reply", strings.NewReader(`{"body":"use postgres"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "thread-ghi", gotAddress)
	assert.Equal(t, "use postgres", gotBody)
}

func TestAPIRunThreadReply_EmptyBody(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	h, err := NewHandler(store, store, WithReplyThreadFunc(func(ctx context.Context, address, body string) error {
		t.Fatal("reply func should not be called for an empty body")
		return nil
	}))
	require.NoError(t, err)

	seedParkedRun(t, store, "parked-run-5", "thread-jkl")

	req := httptest.NewRequest("POST", "/api/runs/parked-run-5/thread/reply", strings.NewReader(`{"body":"  "}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIStopRun_Parked(t *testing.T) {
	// A parked run's container has already been torn down (see
	// handleStepParked); stopping it goes through the daemon-level
	// stopRunFn path, mirroring srv.StopRun rather than a container-manager
	// fallback that expects a live container.
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	var gotTaskID string
	h, err := NewHandler(store, store, WithStopRunFunc(func(ctx context.Context, taskID string) error {
		gotTaskID = taskID
		run, err := store.GetRun(ctx, "parked-run-6")
		if err != nil {
			return err
		}
		run.Complete(domain.RunStateCancelled)
		return store.UpdateRun(ctx, run)
	}))
	require.NoError(t, err)

	seedParkedRun(t, store, "parked-run-6", "thread-mno")

	req := httptest.NewRequest("POST", "/api/runs/parked-run-6/stop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "task-parked-run-6", gotTaskID)

	updated, err := store.GetRun(context.Background(), "parked-run-6")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateCancelled, updated.State)
}
