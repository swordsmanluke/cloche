package web

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/dra"
	"github.com/swordsmanluke/cloche/internal/draauth"
	"github.com/swordsmanluke/cloche/internal/host"
)

// scopeRecorder records the project and DRA the handler passed on: they must
// come from the credential, never from the request.
type scopeRecorder struct {
	project, dra string
	req          dra.WaitRequest
	cancelled    string
}

func (s *scopeRecorder) RegisterDRAWait(_ context.Context, p, n string, r dra.WaitRequest) (string, error) {
	s.project, s.dra, s.req = p, n, r
	if r.Poll == "nope" {
		return "", fmt.Errorf("unknown poll nope; this DRA has: a")
	}
	return "w-1", nil
}
func (s *scopeRecorder) ListDRAWaits(_ context.Context, p, n string) ([]dra.Wait, error) {
	s.project, s.dra = p, n
	return nil, nil
}
func (s *scopeRecorder) CancelDRAWait(_ context.Context, p, n, id string) error {
	s.project, s.dra, s.cancelled = p, n, id
	if id == "missing" {
		return fmt.Errorf("no pending wait missing")
	}
	return nil
}

func TestDRA_WaitRoutes(t *testing.T) {
	e := setupDRA(t)
	rec := &scopeRecorder{}
	e.h.draWaits = rec

	for _, rt := range [][3]string{{"POST", "/api/dra/waits", `{"poll":"a","note":"n"}`}, {"GET", "/api/dra/waits", ""}, {"POST", "/api/dra/waits/cancel", `{"id":"w-1"}`}} {
		assert.Equal(t, http.StatusUnauthorized, e.call(rt[0], rt[1], "", rt[2]).Code, rt[1]+" without a credential")
		assert.Equal(t, http.StatusUnauthorized, e.call(rt[0], rt[1], "bogus", rt[2]).Code, rt[1]+" with a bad credential")
	}
	assert.Empty(t, rec.dra, "nothing reached the registry unauthenticated")

	// A body naming another DRA or project is ignored: scope is the credential's.
	w := e.post("/api/dra/waits", `{"poll":"a","note":"n","interval":300000000000,"dra":"engine","project":"/other"}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"id":"w-1"}`, w.Body.String())
	assert.Equal(t, "auth", rec.dra)
	assert.Equal(t, draProject, rec.project)
	assert.Equal(t, "a", rec.req.Poll)

	w = e.post("/api/dra/waits", `{"poll":"nope","note":"n"}`)
	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "unknown poll nope")

	w = e.call("GET", "/api/dra/waits", e.token, "")
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `[]`, w.Body.String())

	assert.Equal(t, http.StatusOK, e.post("/api/dra/waits/cancel", `{"id":"w-1"}`).Code)
	assert.Equal(t, http.StatusNotFound, e.post("/api/dra/waits/cancel", `{"id":"missing"}`).Code)
	assert.Equal(t, http.StatusBadRequest, e.post("/api/dra/waits/cancel", `{}`).Code)
}

func TestDRA_WaitRoutesNeedAnActiveRun(t *testing.T) {
	e := setupDRA(t)
	e.h.draWaits = &scopeRecorder{}
	seedRunWithProject(t, e.store, "ended", "dra-wake", "succeeded", draProject)
	tok := draauth.Token(draSecret, draauth.Claims{RunID: "ended", Project: draProject, DRA: "auth"})
	assert.Equal(t, http.StatusUnauthorized, e.call("GET", "/api/dra/waits", tok, "").Code)
}

func TestDRA_QuestionsRoutes(t *testing.T) {
	e := setupDRA(t)
	tp := &qaProvider{}
	e.h.taskProvider = tp

	for _, p := range []string{"ask", "await", "read"} {
		assert.Equal(t, http.StatusUnauthorized, e.call("POST", "/api/dra/questions/"+p, "", `{}`).Code, p)
	}
	assert.Empty(t, tp.slot, "no slot may run without a credential")

	tp.out = map[string]string{"question_ids": "Q1\nQ2"}
	w := e.post("/api/dra/questions/ask", `{"file":{"title":"T","questions":[{"prompt":"p"}]}}`)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"ids":["Q1","Q2"]}`, w.Body.String())
	assert.Equal(t, host.SlotAsk, tp.slot)

	tp.out = map[string]string{"questions_open": "Q2"}
	w = e.post("/api/dra/questions/await", `{"ids":["Q1","Q2"]}`)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"open":["Q2"]}`, w.Body.String())

	// The guide is not part of what a DRA may call.
	assert.Equal(t, http.StatusNotFound, e.call("GET", "/api/dra/questions/guide", e.token, "").Code)
}
