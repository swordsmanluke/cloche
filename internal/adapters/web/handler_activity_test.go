package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupHandlerWithActivity(t *testing.T) (*Handler, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	h, err := NewHandler(store, store, WithActivityStore(store))
	require.NoError(t, err)
	return h, store
}

func seedActivity(t *testing.T, store *sqlite.Store, dir string, e activitylog.Entry) {
	t.Helper()
	require.NoError(t, store.AppendActivityEntry(context.Background(), dir, e))
}

func TestAPIActivity_NoStoreConfigured(t *testing.T) {
	h, _ := setupHandler(t) // no WithActivityStore

	req := httptest.NewRequest("GET", "/api/activity", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp activityStreamResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Empty(t, resp.Entries)
}

func TestAPIActivity_Empty(t *testing.T) {
	h, _ := setupHandlerWithActivity(t)

	req := httptest.NewRequest("GET", "/api/activity", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp activityStreamResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Empty(t, resp.Entries)
	assert.Empty(t, resp.Cursor)
}

func TestAPIActivity_UnknownProject(t *testing.T) {
	h, _ := setupHandlerWithActivity(t)

	req := httptest.NewRequest("GET", "/api/activity?project=nope", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIActivity_ReturnsNewestFirst(t *testing.T) {
	h, store := setupHandlerWithActivity(t)
	seedRunWithProject(t, store, "run-1", "develop", domain.RunStateSucceeded, "/proj/one")
	now := time.Now()

	seedActivity(t, store, "/proj/one", activitylog.Entry{
		Timestamp: now, Kind: activitylog.KindAttemptStarted, WorkflowName: "develop",
	})
	seedActivity(t, store, "/proj/one", activitylog.Entry{
		Timestamp: now.Add(time.Second), Kind: activitylog.KindAttemptEnded, WorkflowName: "develop", State: "succeeded",
	})

	req := httptest.NewRequest("GET", "/api/activity?project=one", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp activityStreamResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Entries, 2)
	assert.Equal(t, "develop succeeded", resp.Entries[0].Text)
	assert.True(t, resp.Entries[0].Failure == false)
	assert.Equal(t, "develop started", resp.Entries[1].Text)
}

func TestAPIActivity_CollapsesRepeatsWithOrdinal(t *testing.T) {
	h, store := setupHandlerWithActivity(t)
	seedRunWithProject(t, store, "run-1", "intent-scan", domain.RunStateFailed, "/proj/one")
	now := time.Now()

	for i := 0; i < 8; i++ {
		seedActivity(t, store, "/proj/one", activitylog.Entry{
			Timestamp:    now.Add(time.Duration(i) * time.Minute),
			Kind:         activitylog.KindAttemptEnded,
			WorkflowName: "intent-scan",
			State:        "failed",
		})
	}

	req := httptest.NewRequest("GET", "/api/activity?project=one", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp activityStreamResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Entries, 1, "8 repeats of the same signature should collapse into one line")
	line := resp.Entries[0]
	assert.Equal(t, 8, line.Count)
	assert.Equal(t, "intent-scan failed · 8th today", line.Text)
	assert.True(t, line.Failure)
}

func TestAPIActivity_DoesNotCollapseAcrossDifferentSignatures(t *testing.T) {
	h, store := setupHandlerWithActivity(t)
	seedRunWithProject(t, store, "run-1", "develop", domain.RunStateSucceeded, "/proj/one")
	now := time.Now()

	seedActivity(t, store, "/proj/one", activitylog.Entry{
		Timestamp: now, Kind: activitylog.KindAttemptEnded, WorkflowName: "develop", State: "failed",
	})
	seedActivity(t, store, "/proj/one", activitylog.Entry{
		Timestamp: now.Add(time.Minute), Kind: activitylog.KindAttemptEnded, WorkflowName: "release", State: "failed",
	})

	req := httptest.NewRequest("GET", "/api/activity?project=one", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp activityStreamResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Entries, 2)
	assert.Equal(t, "release failed", resp.Entries[0].Text)
	assert.Equal(t, "develop failed", resp.Entries[1].Text)
}

func TestAPIActivity_FailuresOnlyFilter(t *testing.T) {
	h, store := setupHandlerWithActivity(t)
	seedRunWithProject(t, store, "run-1", "develop", domain.RunStateSucceeded, "/proj/one")
	now := time.Now()

	seedActivity(t, store, "/proj/one", activitylog.Entry{
		Timestamp: now, Kind: activitylog.KindAttemptEnded, WorkflowName: "develop", State: "succeeded",
	})
	seedActivity(t, store, "/proj/one", activitylog.Entry{
		Timestamp: now.Add(time.Minute), Kind: activitylog.KindAttemptEnded, WorkflowName: "develop", State: "failed",
	})

	req := httptest.NewRequest("GET", "/api/activity?project=one&failures_only=1", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp activityStreamResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Entries, 1)
	assert.Equal(t, "develop failed", resp.Entries[0].Text)
}

func TestAPIActivity_AllProjectsMergesAndLabels(t *testing.T) {
	h, store := setupHandlerWithActivity(t)
	seedRunWithProject(t, store, "run-1", "develop", domain.RunStateSucceeded, "/proj/alpha")
	seedRunWithProject(t, store, "run-2", "develop", domain.RunStateSucceeded, "/proj/beta")
	now := time.Now()

	seedActivity(t, store, "/proj/alpha", activitylog.Entry{
		Timestamp: now, Kind: activitylog.KindAttemptStarted, WorkflowName: "develop",
	})
	seedActivity(t, store, "/proj/beta", activitylog.Entry{
		Timestamp: now.Add(time.Minute), Kind: activitylog.KindAttemptStarted, WorkflowName: "develop",
	})

	req := httptest.NewRequest("GET", "/api/activity", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var resp activityStreamResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Entries, 2)
	assert.Equal(t, "beta", resp.Entries[0].ProjectLabel)
	assert.Equal(t, "alpha", resp.Entries[1].ProjectLabel)
}

func TestAPIActivity_CursorPagination(t *testing.T) {
	h, store := setupHandlerWithActivity(t)
	seedRunWithProject(t, store, "run-1", "develop", domain.RunStateSucceeded, "/proj/one")
	now := time.Now()

	for i := 0; i < 3; i++ {
		seedActivity(t, store, "/proj/one", activitylog.Entry{
			Timestamp:    now.Add(time.Duration(i) * time.Minute),
			Kind:         activitylog.KindStepCompleted,
			WorkflowName: "develop",
			StepName:     fmt.Sprintf("step-%d", i),
			Result:       "success",
		})
	}

	req := httptest.NewRequest("GET", "/api/activity?project=one&limit=2", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	var page1 activityStreamResponse
	require.NoError(t, json.NewDecoder(w.Body).Decode(&page1))
	require.Len(t, page1.Entries, 2)
	require.NotEmpty(t, page1.Cursor)

	req2 := httptest.NewRequest("GET", "/api/activity?project=one&limit=2&before="+page1.Cursor, nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)

	var page2 activityStreamResponse
	require.NoError(t, json.NewDecoder(w2.Body).Decode(&page2))
	require.Len(t, page2.Entries, 1)
	assert.Empty(t, page2.Cursor)
}
