package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/signal"
)

type qaProvider struct {
	trackerTaskProvider
	answerPath string
	guide      string
}

func (q *qaProvider) QAGuide(_ string, judge bool) (string, error) {
	if judge {
		return "judge", nil
	}
	return q.guide, nil
}

func TestAPIQuestions(t *testing.T) {
	h, store := setupHandler(t)
	run := domain.NewRun("run-1", "develop")
	run.ProjectDir = "/home/user/projects/myapp"
	require.NoError(t, store.CreateRun(context.Background(), run))
	h.signalTokens = signal.NewTokenStore(t.TempDir())
	token, err := h.signalTokens.Get(run.ProjectDir)
	require.NoError(t, err)

	answer := filepath.Join(t.TempDir(), "a.txt")
	require.NoError(t, os.WriteFile(answer, []byte("use postgres\n"), 0o644))
	tp := &qaProvider{guide: "ask well"}
	h.taskProvider = tp

	do := func(method, path, body, auth string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/projects/myapp/questions/"+path, strings.NewReader(body))
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	for _, rt := range [][3]string{{"POST", "ask", `{"file":{}}`}, {"POST", "await", `{"ids":["Q1"]}`}, {"POST", "read", `{"id":"Q1"}`}, {"GET", "guide", ``}} {
		for _, auth := range []string{"", "Bearer nope"} {
			w := do(rt[0], rt[1], rt[2], auth)
			require.Equal(t, http.StatusUnauthorized, w.Code, "%s %s auth=%q", rt[0], rt[1], auth)
		}
	}
	require.Empty(t, tp.slot, "no slot may run without a valid token")

	tp.out = map[string]string{"question_ids": "Q1\nQ2"}
	w := do("POST", "ask", `{"file":{"title":"T","questions":[{"prompt":"p"}]}}`, "Bearer "+token)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.JSONEq(t, `{"ids":["Q1","Q2"]}`, w.Body.String())
	assert.Equal(t, host.SlotAsk, tp.slot)
	assert.NotEmpty(t, tp.inputs["questions_file"])
	_, statErr := os.Stat(tp.inputs["questions_file"])
	assert.True(t, os.IsNotExist(statErr), "temp question file is removed")

	tp.out = map[string]string{"questions_open": "Q2"}
	w = do("POST", "await", `{"ids":["Q1","Q2"]}`, "Bearer "+token)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"open":["Q2"]}`, w.Body.String())
	assert.Equal(t, "Q1\nQ2", tp.inputs["question_ids"])

	tp.out = map[string]string{"questions_open": ""}
	w = do("POST", "await", `{"ids":["Q1"]}`, "Bearer "+token)
	assert.JSONEq(t, `{"open":[]}`, w.Body.String())

	tp.out = map[string]string{"answer_path": answer}
	w = do("POST", "read", `{"id":"Q1"}`, "Bearer "+token)
	require.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"answer":"use postgres\n"}`, w.Body.String())

	tp.out, tp.err = nil, host.ErrPhaseFailed
	w = do("POST", "read", `{"id":"Q2"}`, "Bearer "+token)
	assert.Equal(t, http.StatusNotFound, w.Code)

	tp.err = host.ErrNoQABind
	w = do("POST", "ask", `{"file":{}}`, "Bearer "+token)
	assert.Equal(t, http.StatusConflict, w.Code)
	var e struct{ Error string }
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &e))
	assert.Contains(t, e.Error, "bind qa = <module>")

	w = do("GET", "guide", "", "Bearer "+token)
	assert.Equal(t, "ask well", w.Body.String())
	req := httptest.NewRequest("GET", "/api/projects/myapp/questions/guide?judge=1", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	assert.Equal(t, "judge", w.Body.String())
}
