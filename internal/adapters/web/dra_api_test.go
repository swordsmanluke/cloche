package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/draauth"
	"github.com/swordsmanluke/cloche/internal/signal"
)

const draProject = "/home/user/projects/myapp"

var draSecret = []byte("dra-test-secret")

// fakeTracker is an in-memory tracker behind the tracker phases.
type fakeTracker struct {
	mockTaskProvider
	labels map[string][]string
	calls  []string // "slot:action"
	n      int
}

func (f *fakeTracker) RunTrackerPhase(_ context.Context, project, slot string, in map[string]string) (map[string]string, error) {
	if project != draProject {
		return nil, fmt.Errorf("wrong project %q", project)
	}
	f.calls = append(f.calls, slot+":"+in["action"])
	switch slot {
	case "create-task":
		f.n++
		id := fmt.Sprintf("new-%d", f.n)
		f.labels[id] = splitLabels(in["labels"])
		return map[string]string{"new_task_id": id}, nil
	case "update-task":
		switch in["action"] {
		case "get":
			l, ok := f.labels[in["target_task_id"]]
			if !ok {
				return map[string]string{"task_found": "false"}, nil
			}
			return map[string]string{"task_found": "true", "task_labels": strings.Join(l, ",")}, nil
		case "find":
			return map[string]string{"task_found": "true", "found_task_id": "other-1"}, nil
		case "update":
			return map[string]string{"task_found": "true"}, nil
		}
	}
	return nil, fmt.Errorf("unexpected %s", slot)
}

type draEnv struct {
	h       *Handler
	store   *sqlite.Store
	tracker *fakeTracker
	token   string
}

func setupDRA(t *testing.T) *draEnv {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	h, err := NewHandler(store, store, WithDRAAccess(draSecret),
		WithHelpMCP(draSecret, func(context.Context, string, string, string, string, []string, string) (string, string, bool, error) {
			return "", "", false, nil
		}),
		WithSignalIntake(signal.NewTokenStore(t.TempDir()), &signal.Dispatcher{Store: store}))
	require.NoError(t, err)
	tr := &fakeTracker{labels: map[string][]string{
		"mine-1":  {"auth", "bug"},
		"other-1": {"engine"},
		"bare-1":  {},
	}}
	tr.tasks = map[string][]TaskEntry{draProject: {{ID: "mine-1", Title: "t"}}}
	h.taskProvider = tr

	seedRunWithProject(t, store, "wake-1", "dra-wake", domain.RunStateRunning, draProject)
	seedRunWithProject(t, store, "run-a", "develop", domain.RunStateSucceeded, draProject)
	seedRunWithProject(t, store, "run-b", "develop", domain.RunStateSucceeded, "/home/user/projects/other")
	return &draEnv{h: h, store: store, tracker: tr,
		token: draauth.Token(draSecret, draauth.Claims{RunID: "wake-1", Project: draProject, DRA: "auth"})}
}

func (e *draEnv) call(method, path, token, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "172.17.0.2:4000" // a container, not loopback
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func (e *draEnv) post(path, body string) *httptest.ResponseRecorder {
	return e.call("POST", path, e.token, body)
}

func outputs(t *testing.T, rec *httptest.ResponseRecorder) map[string]string {
	t.Helper()
	var r apiTrackerPhaseResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &r), rec.Body.String())
	return r.Outputs
}

func TestDRA_ReadsAllowed(t *testing.T) {
	e := setupDRA(t)

	rec := e.call("GET", "/api/dra/tasks", e.token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "mine-1")

	rec = e.call("GET", "/api/dra/runs", e.token, "")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "run-a")
	assert.NotContains(t, rec.Body.String(), "run-b", "another project's runs are not listed")

	rec = e.call("GET", "/api/dra/runs/run-a", e.token, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "run-a")

	rec = e.call("GET", "/api/dra/runs/run-a/steps/build/output", e.token, "")
	assert.NotEqual(t, http.StatusUnauthorized, rec.Code)
	assert.NotEqual(t, http.StatusForbidden, rec.Code)
}

func TestDRA_CannotReadAnotherProjectsRun(t *testing.T) {
	e := setupDRA(t)
	for _, p := range []string{"/api/dra/runs/run-b", "/api/dra/runs/run-b/steps/build/output", "/api/dra/runs/nope"} {
		assert.Equal(t, http.StatusNotFound, e.call("GET", p, e.token, "").Code, p)
	}
}

func TestDRA_CreateAlwaysCarriesDomainLabel(t *testing.T) {
	e := setupDRA(t)
	rec := e.post("/api/dra/tasks/create", `{"inputs":{"title":"x","labels":"perf"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	id := outputs(t, rec)["new_task_id"]
	assert.ElementsMatch(t, []string{"perf", "auth"}, e.tracker.labels[id])

	rec = e.post("/api/dra/tasks/create", `{"inputs":{"title":"y"}}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, []string{"auth"}, e.tracker.labels[outputs(t, rec)["new_task_id"]])
}

func TestDRA_CreateCannotSetReadyOrUnknownInputs(t *testing.T) {
	e := setupDRA(t)
	assert.Equal(t, http.StatusForbidden, e.post("/api/dra/tasks/create", `{"inputs":{"title":"x","labels":"ready"}}`).Code)
	assert.Equal(t, http.StatusBadRequest, e.post("/api/dra/tasks/create", `{"inputs":{"title":"x","target_task_id":"other-1"}}`).Code)
	assert.Empty(t, e.tracker.calls)
}

func TestDRA_UpdateOwnTask(t *testing.T) {
	e := setupDRA(t)
	rec := e.post("/api/dra/tasks/update", `{"inputs":{"action":"update","target_task_id":"mine-1","comment":"hi"}}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, e.tracker.calls, "update-task:update")
}

func TestDRA_CannotTouchAnotherDomainsTask(t *testing.T) {
	e := setupDRA(t)
	for _, id := range []string{"other-1", "bare-1", "missing-1"} {
		rec := e.post("/api/dra/tasks/update", fmt.Sprintf(`{"inputs":{"action":"update","target_task_id":%q,"comment":"hi"}}`, id))
		assert.Equal(t, http.StatusForbidden, rec.Code, "update %s", id)
		rec = e.post("/api/dra/tasks/close", fmt.Sprintf(`{"task_id":%q}`, id))
		assert.Equal(t, http.StatusForbidden, rec.Code, "close %s", id)
	}
	assert.NotContains(t, e.tracker.calls, "update-task:update")
	assert.Empty(t, e.tracker.closedTask)

	// find does not reveal another domain's task either.
	rec := e.post("/api/dra/tasks/update", `{"inputs":{"action":"find","signal_source":"ci","signal_key":"k"}}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "false", outputs(t, rec)["task_found"])
}

func TestDRA_UpdateLabelRules(t *testing.T) {
	e := setupDRA(t)
	rec := e.post("/api/dra/tasks/update", `{"inputs":{"action":"update","target_task_id":"mine-1","add_labels":"ready"}}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = e.post("/api/dra/tasks/update", `{"inputs":{"action":"update","target_task_id":"mine-1","remove_labels":"auth"}}`)
	assert.Equal(t, http.StatusForbidden, rec.Code)
	rec = e.post("/api/dra/tasks/update", `{"inputs":{"action":"update","comment":"x"}}`)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.NotContains(t, e.tracker.calls, "update-task:update")
}

func TestDRA_CloseOwnTask(t *testing.T) {
	e := setupDRA(t)
	rec := e.post("/api/dra/tasks/close", `{"task_id":"mine-1"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "mine-1", e.tracker.closedTask)
}

func TestDRA_CredentialStopsWhenRunEnds(t *testing.T) {
	e := setupDRA(t)
	require.Equal(t, http.StatusOK, e.call("GET", "/api/dra/tasks", e.token, "").Code)

	for _, state := range []domain.RunState{domain.RunStateParked, domain.RunStateSucceeded, domain.RunStateFailed, domain.RunStateCancelled} {
		run, err := e.store.GetRun(context.Background(), "wake-1")
		require.NoError(t, err)
		run.State = state
		require.NoError(t, e.store.UpdateRun(context.Background(), run))
		for _, c := range [][3]string{
			{"GET", "/api/dra/tasks", ""},
			{"GET", "/api/dra/runs", ""},
			{"GET", "/api/dra/runs/run-a", ""},
			{"POST", "/api/dra/tasks/create", `{"inputs":{"title":"x"}}`},
			{"POST", "/api/dra/tasks/close", `{"task_id":"mine-1"}`},
		} {
			assert.Equal(t, http.StatusUnauthorized, e.call(c[0], c[1], e.token, c[2]).Code, "%s %s in state %s", c[0], c[1], state)
		}
	}
	assert.Empty(t, e.tracker.calls)
}

func TestDRA_BadCredentialsRefused(t *testing.T) {
	e := setupDRA(t)
	forTwo := draauth.Token(draSecret, draauth.Claims{RunID: "run-a", Project: draProject, DRA: "auth"}) // run not running
	wrongProject := draauth.Token(draSecret, draauth.Claims{RunID: "wake-1", Project: "/home/user/projects/other", DRA: "auth"})
	forged := draauth.Token([]byte("not-the-secret"), draauth.Claims{RunID: "wake-1", Project: draProject, DRA: "auth"})
	for name, tok := range map[string]string{"none": "", "garbage": "x.y", "forged": forged, "finished run": forTwo, "run of another project": wrongProject} {
		assert.Equal(t, http.StatusUnauthorized, e.call("GET", "/api/dra/tasks", tok, "").Code, name)
	}
}

// A DRA credential does not pass for another route's credential: the
// project signal token or a run's MCP token. The rest of the dashboard has no
// auth at all for now (cloche runs locally; auth is a later story), so those
// routes do not refuse a DRA.
func TestDRA_CredentialIsNotAnotherRoutesCredential(t *testing.T) {
	e := setupDRA(t)
	for _, c := range [][2]string{
		{"POST", "/api/projects/" + url.PathEscape(draProject) + "/tasks/create"},
		{"POST", "/api/projects/" + url.PathEscape(draProject) + "/tasks/update"},
		{"POST", "/api/projects/" + url.PathEscape(draProject) + "/signals"},
		{"POST", "/api/projects/other/tasks/create"},
		{"POST", "/mcp?run_id=wake-1"},
	} {
		assert.Equal(t, http.StatusUnauthorized, e.call(c[0], c[1], e.token, "{}").Code, "%s %s", c[0], c[1])
	}
	assert.Empty(t, e.tracker.calls)
	assert.Empty(t, e.tracker.closedTask)
}
