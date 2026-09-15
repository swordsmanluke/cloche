package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/intent"
)

// setupIntentProject creates a temp project dir seeded with a run (so
// resolveProjectDir can find it) and returns the dir and its label.
func setupIntentProject(t *testing.T) (*Handler, string, string) {
	t.Helper()
	h, store := setupHandler(t)
	dir := t.TempDir()
	seedRunWithProject(t, store, "run-1", "develop", domain.RunStateSucceeded, dir)
	return h, dir, filepath.Base(dir)
}

func TestAPIIntentRequirements_NoIntentDir(t *testing.T) {
	h, _, label := setupIntentProject(t)

	req := httptest.NewRequest("GET", "/api/projects/"+label+"/intent/requirements", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp apiRequirementsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Requirements)
	assert.Empty(t, resp.LastScanAt)
}

func TestAPIIntentRequirements_List(t *testing.T) {
	h, dir, label := setupIntentProject(t)
	store := intent.NewStore(dir)

	created, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelDomain, Domains: []string{"billing"}},
		Confidence: intent.ConfidenceHigh,
		Body:       "Never log raw card numbers.",
		Provenance: intent.Provenance{Kind: intent.ProvenanceTranscript, Ref: "run-42"},
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/projects/"+label+"/intent/requirements", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var resp apiRequirementsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(t, resp.Requirements, 1)

	got := resp.Requirements[0]
	assert.Equal(t, created.ID, got.ID)
	assert.Equal(t, "active", got.Status)
	assert.Equal(t, "domain", got.Scope.Level)
	assert.Equal(t, []string{"billing"}, got.Scope.Domains)
	assert.Equal(t, "Never log raw card numbers.", got.Statement)
	assert.Equal(t, "/runs/run-42", got.Provenance.Link)
	assert.False(t, got.NewSinceScan)
}

func TestAPIIntentRequirements_NewSinceScan(t *testing.T) {
	h, dir, label := setupIntentProject(t)
	store := intent.NewStore(dir)

	require.NoError(t, store.SaveScanState(&intent.ScanState{LastScanAt: time.Now().Add(-1 * time.Hour)}))

	old, err := store.CreateRequirement(&intent.Requirement{
		Status: intent.StatusActive, Scope: intent.Scope{Level: intent.ScopeLevelProject}, Body: "old",
	})
	require.NoError(t, err)
	old.Created = time.Now().Add(-2 * time.Hour)
	require.NoError(t, store.SaveRequirement(old))

	_, err = store.CreateRequirement(&intent.Requirement{
		Status: intent.StatusActive, Scope: intent.Scope{Level: intent.ScopeLevelProject}, Body: "new",
	})
	require.NoError(t, err)

	req := httptest.NewRequest("GET", "/api/projects/"+label+"/intent/requirements", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	var resp apiRequirementsResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(t, resp.LastScanAt)

	byStatement := map[string]apiRequirement{}
	for _, r := range resp.Requirements {
		byStatement[r.Statement] = r
	}
	assert.False(t, byStatement["old"].NewSinceScan)
	assert.True(t, byStatement["new"].NewSinceScan)
}

func TestAPIIntentRequirementsPatch_RoundTrips(t *testing.T) {
	h, dir, label := setupIntentProject(t)
	store := intent.NewStore(dir)

	created, err := store.CreateRequirement(&intent.Requirement{
		Status:     intent.StatusActive,
		Scope:      intent.Scope{Level: intent.ScopeLevelProject},
		Confidence: intent.ConfidenceMedium,
		Body:       "Original statement.",
	})
	require.NoError(t, err)
	require.False(t, created.UserEdited)

	body := `{"id":"` + created.ID + `","statement":"Edited statement.","scope":{"level":"domain","domains":["api"]}}`
	req := httptest.NewRequest("PATCH", "/api/projects/"+label+"/intent/requirements", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got apiRequirement
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "Edited statement.", got.Statement)
	assert.Equal(t, "domain", got.Scope.Level)
	assert.True(t, got.UserEdited)

	// Visible in the file store (round-trips through disk, per acceptance).
	onDisk, err := store.GetRequirement(created.ID)
	require.NoError(t, err)
	assert.Equal(t, "Edited statement.", onDisk.Body)
	assert.True(t, onDisk.UserEdited)
	assert.Equal(t, []string{"api"}, onDisk.Scope.Domains)
}

func TestAPIIntentRequirementsPatch_StatusOnlyDoesNotSetUserEdited(t *testing.T) {
	h, dir, label := setupIntentProject(t)
	store := intent.NewStore(dir)

	created, err := store.CreateRequirement(&intent.Requirement{
		Status: intent.StatusActive, Scope: intent.Scope{Level: intent.ScopeLevelProject}, Body: "x",
	})
	require.NoError(t, err)

	body := `{"id":"` + created.ID + `","status":"disabled"}`
	req := httptest.NewRequest("PATCH", "/api/projects/"+label+"/intent/requirements", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	var got apiRequirement
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "disabled", got.Status)
	assert.False(t, got.UserEdited)
}

func TestAPIIntentRequirementsPatch_UnknownID(t *testing.T) {
	h, _, label := setupIntentProject(t)

	body := `{"id":"req-dead","statement":"x"}`
	req := httptest.NewRequest("PATCH", "/api/projects/"+label+"/intent/requirements", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIIntentRequirementsPatch_MissingID(t *testing.T) {
	h, _, label := setupIntentProject(t)

	req := httptest.NewRequest("PATCH", "/api/projects/"+label+"/intent/requirements", bytes.NewBufferString(`{"statement":"x"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestAPIIntentDomains_GetAndPut(t *testing.T) {
	h, _, label := setupIntentProject(t)

	// Empty by default (dormancy guarantee).
	req := httptest.NewRequest("GET", "/api/projects/"+label+"/intent/domains", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	var dm apiDomainMap
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &dm))
	assert.Equal(t, 1, dm.Version)
	assert.Empty(t, dm.Domains)

	putBody := `{"version":1,"domains":[{"name":"billing","description":"Payments","paths":["billing/**"],"user_edited":true}]}`
	req = httptest.NewRequest("PUT", "/api/projects/"+label+"/intent/domains", bytes.NewBufferString(putBody))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	req = httptest.NewRequest("GET", "/api/projects/"+label+"/intent/domains", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &dm))
	require.Len(t, dm.Domains, 1)
	assert.Equal(t, "billing", dm.Domains[0].Name)
	assert.Equal(t, []string{"billing/**"}, dm.Domains[0].Paths)
	assert.True(t, dm.Domains[0].UserEdited)
}

func TestAPIIntentScan_NotConfigured(t *testing.T) {
	h, _, label := setupIntentProject(t)

	req := httptest.NewRequest("POST", "/api/projects/"+label+"/intent/scan", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)
}

func TestAPIIntentScan_Dispatches(t *testing.T) {
	h, store := setupHandler(t)
	dir := t.TempDir()
	seedRunWithProject(t, store, "run-1", "develop", domain.RunStateSucceeded, dir)
	label := filepath.Base(dir)

	var gotDir string
	h.scanFn = func(ctx context.Context, projectDir string) (string, error) {
		gotDir = projectDir
		return "run-scan-1", nil
	}

	req := httptest.NewRequest("POST", "/api/projects/"+label+"/intent/scan", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, dir, gotDir)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "run-scan-1", resp["run_id"])
}

func TestAPIIntentDoc_ServesContent(t *testing.T) {
	h, dir, label := setupIntentProject(t)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "docs", "notes.md"), []byte("# Heading\n\nBody"), 0o644))

	req := httptest.NewRequest("GET", "/api/projects/"+label+"/intent/doc?path=docs/notes.md", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "# Heading\n\nBody", w.Body.String())
}

func TestAPIIntentDoc_RejectsTraversal(t *testing.T) {
	h, _, label := setupIntentProject(t)

	req := httptest.NewRequest("GET", "/api/projects/"+label+"/intent/doc?path=../../../../etc/passwd", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusOK, w.Code)
}

func TestProvenanceLink(t *testing.T) {
	cases := []struct {
		name string
		p    intent.Provenance
		want string
	}{
		{"transcript", intent.Provenance{Kind: intent.ProvenanceTranscript, Ref: "run-1"}, "/runs/run-1"},
		{"prompt", intent.Provenance{Kind: intent.ProvenancePrompt, Ref: "task-1"}, "/tasks/task-1"},
		{"commit", intent.Provenance{Kind: intent.ProvenanceCommit, Ref: "abc123"}, "/api/projects/my-proj/info/prompt-diff?sha=abc123"},
		{"doc", intent.Provenance{Kind: intent.ProvenanceDoc, Ref: "docs/x.md"}, "/api/projects/my-proj/intent/doc?path=docs%2Fx.md"},
		{"user, no ref", intent.Provenance{Kind: intent.ProvenanceUser, Ref: ""}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, provenanceLink("my-proj", tc.p))
		})
	}
}
