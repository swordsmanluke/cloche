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

func TestAPIStepInput_ListsAttemptsAndIsSeparateFromOutput(t *testing.T) {
	h, store := setupHandler(t)
	seedRun(t, store, "run-1", "develop", domain.RunStateRunning)
	ctx := context.Background()
	require.NoError(t, store.SaveStepInput(ctx, &domain.StepInput{RunID: "run-1", StepName: "implement", Kind: domain.StepInputPrompt, Content: "Do the thing.\n"}))
	require.NoError(t, store.SaveStepInput(ctx, &domain.StepInput{RunID: "run-1", StepName: "implement", Kind: domain.StepInputPrompt, Content: "Try again.\n"}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs/run-1/steps/implement/input", nil))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var body struct {
		RunID    string         `json:"run_id"`
		StepName string         `json:"step_name"`
		Inputs   []apiStepInput `json:"inputs"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "run-1", body.RunID)
	assert.Equal(t, "implement", body.StepName)
	require.Len(t, body.Inputs, 2)
	assert.Equal(t, 1, body.Inputs[0].Attempt)
	assert.Equal(t, "prompt", body.Inputs[0].Kind)
	assert.Equal(t, "Do the thing.\n", body.Inputs[0].Content)
	assert.Equal(t, 2, body.Inputs[1].Attempt)
	assert.NotEmpty(t, body.Inputs[1].CreatedAt)

	// A step with nothing recorded is an empty list, not a 404.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs/run-1/steps/test/input", nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Empty(t, body.Inputs)

	// The prompt never leaks into the step's output endpoint.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs/run-1/steps/implement/output", nil))
	assert.NotContains(t, rec.Body.String(), "Do the thing.")

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/runs/nope/steps/implement/input", nil))
	assert.Equal(t, http.StatusNotFound, rec.Code)
}
