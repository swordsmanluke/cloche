package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAPITaskAttempts_NotFound(t *testing.T) {
	h, store := setupHandler(t)
	dir := t.TempDir()
	seedRunWithProject(t, store, "ta-seed", "develop", domain.RunStateSucceeded, dir)

	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/tasks/no-such-task/attempts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPITaskAttempts_SingleAttempt(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()
	dir := t.TempDir()

	run := domain.NewRun("main-ta01-develop", "develop")
	run.ProjectDir = dir
	run.TaskID = "task-attempts-1"
	run.TaskTitle = "Do the thing"
	run.AttemptID = "ta01"
	run.Start()
	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/tasks/task-attempts-1/attempts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp apiTaskAttempts
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, "task-attempts-1", resp.TaskID)
	assert.Equal(t, "Do the thing", resp.Title)
	assert.Equal(t, "succeeded", resp.Status)
	require.Len(t, resp.Attempts, 1)
	assert.Equal(t, 1, resp.Attempts[0].AttemptNum)
	assert.Equal(t, "ta01", resp.Attempts[0].AttemptID)
	assert.Equal(t, "main-ta01-develop", resp.Attempts[0].RunID)
	assert.Equal(t, "succeeded", resp.Attempts[0].Outcome)
	assert.Empty(t, resp.Attempts[0].RetryReason)
}

func TestAPITaskAttempts_RetryReasonFromPreviousFailure(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()
	dir := t.TempDir()

	first := domain.NewRun("main-ta02-develop", "develop")
	first.ProjectDir = dir
	first.TaskID = "task-attempts-2"
	first.AttemptID = "ta02"
	first.Start()
	require.NoError(t, store.CreateRun(ctx, first))
	require.NoError(t, store.SaveCapture(ctx, first.ID, &domain.StepExecution{
		StepName:    "build",
		Result:      "fail",
		StartedAt:   time.Now().Add(-time.Minute),
		CompletedAt: time.Now(),
	}))
	first.Complete(domain.RunStateFailed)
	require.NoError(t, store.UpdateRun(ctx, first))

	second := domain.NewRun("main-ta03-develop", "develop")
	second.ProjectDir = dir
	second.TaskID = "task-attempts-2"
	second.AttemptID = "ta03"
	second.Start()
	second.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, second))

	req := httptest.NewRequest("GET", "/api/projects/"+filepath.Base(dir)+"/tasks/task-attempts-2/attempts", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp apiTaskAttempts
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Attempts, 2)
	assert.Empty(t, resp.Attempts[0].RetryReason, "first attempt has no prior attempt to retry from")
	assert.Equal(t, "build", resp.Attempts[1].RetryReason)
}

func TestAPIRunBranch_NoneRecorded(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("main-br01-develop", "develop")
	run.TaskID = "task-branch-1"
	run.AttemptID = "br01"
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/runs/main-br01-develop/branch", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Branches []apiRepoBranch `json:"branches"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Empty(t, resp.Branches)
}

func TestAPIRunBranch_SingleRepo(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("main-br02-develop", "develop")
	run.TaskID = "task-branch-2"
	run.AttemptID = "br02"
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.SetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, "child_branch", "cloche/task-branch-2"))

	req := httptest.NewRequest("GET", "/api/runs/main-br02-develop/branch", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Branches []apiRepoBranch `json:"branches"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Branches, 1)
	assert.Equal(t, "cloche/task-branch-2", resp.Branches[0].Branch)
}

func TestAPIRunBranch_MultiRepo(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("main-br03-develop", "develop")
	run.TaskID = "task-branch-3"
	run.AttemptID = "br03"
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.SetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, "child_repos", "api,web"))
	require.NoError(t, store.SetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, "child_branch:api", "cloche/api-branch"))
	require.NoError(t, store.SetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, "child_branch:web", "cloche/web-branch"))

	req := httptest.NewRequest("GET", "/api/runs/main-br03-develop/branch", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Branches []apiRepoBranch `json:"branches"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	require.Len(t, resp.Branches, 2)
	assert.Equal(t, "api", resp.Branches[0].Repo)
	assert.Equal(t, "cloche/api-branch", resp.Branches[0].Branch)
	assert.Equal(t, "web", resp.Branches[1].Repo)
	assert.Equal(t, "cloche/web-branch", resp.Branches[1].Branch)
}

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v: %s", args, out)
	return string(out)
}

func TestAPIRunDiff_NoBranchRecorded(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()
	dir := t.TempDir()

	run := domain.NewRun("main-df01-develop", "develop")
	run.ProjectDir = dir
	run.TaskID = "task-diff-1"
	run.AttemptID = "df01"
	run.BaseSHA = "deadbeef"
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/runs/main-df01-develop/diff", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRunDiff_ReturnsGitDiff(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()
	dir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\n"), 0o644))
	runGitCmd(t, dir, "init")
	runGitCmd(t, dir, "add", ".")
	runGitCmd(t, dir, "commit", "-m", "initial")
	baseSHA := strings.TrimSpace(runGitCmd(t, dir, "rev-parse", "HEAD"))

	runGitCmd(t, dir, "checkout", "-b", "cloche/result-branch")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("base\nchanged\n"), 0o644))
	runGitCmd(t, dir, "commit", "-am", "result change")
	runGitCmd(t, dir, "checkout", "-")

	run := domain.NewRun("main-df02-develop", "develop")
	run.ProjectDir = dir
	run.TaskID = "task-diff-2"
	run.AttemptID = "df02"
	run.BaseSHA = baseSHA
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.SetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, "child_branch", "cloche/result-branch"))

	req := httptest.NewRequest("GET", "/api/runs/main-df02-develop/diff", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "changed")
}

func TestAPIRunConsole_NoContainer(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("main-cn01-develop", "develop")
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/runs/main-cn01-develop/console", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestAPIRunConsole_ReturnsRawContainerLogs(t *testing.T) {
	h, store, mgr := setupHandlerWithContainerManager(t)
	ctx := context.Background()

	run := domain.NewRun("main-cn02-develop", "develop")
	run.ContainerID = "container-xyz"
	require.NoError(t, store.CreateRun(ctx, run))
	mgr.containers[run.ContainerID] = true

	req := httptest.NewRequest("GET", "/api/runs/main-cn02-develop/console", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "mock logs", w.Body.String())
}

func TestAPIRunDetail_PollStepFacts(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("main-pl01-develop", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName:  "wait-for-ci",
		StartedAt: time.Now(),
	}))
	require.NoError(t, store.UpsertPoll(ctx, &ports.PollRecord{
		RunID:      run.ID,
		StepName:   "wait-for-ci",
		StartedAt:  time.Now().Add(-5 * time.Minute),
		LastPollAt: time.Now(),
		PollCount:  3,
	}))

	req := httptest.NewRequest("GET", "/api/runs/main-pl01-develop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var detail apiRunDetail
	require.NoError(t, json.NewDecoder(w.Body).Decode(&detail))
	require.Len(t, detail.Steps, 1)
	assert.Equal(t, 3, detail.Steps[0].PollCount)
	assert.NotEmpty(t, detail.Steps[0].LastPollAt)
}

func TestAPIRunDetail_TokenUsageAggregation(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()

	run := domain.NewRun("main-tk01-develop", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	now := time.Now()
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName: "implement", Result: "success", StartedAt: now.Add(-2 * time.Minute), CompletedAt: now,
		Usage: &domain.TokenUsage{InputTokens: 100, OutputTokens: 50, AgentName: "claude"},
	}))
	require.NoError(t, store.SaveCapture(ctx, run.ID, &domain.StepExecution{
		StepName: "review", Result: "success", StartedAt: now.Add(-time.Minute), CompletedAt: now,
		Usage: &domain.TokenUsage{InputTokens: 40, OutputTokens: 10, AgentName: "claude"},
	}))

	req := httptest.NewRequest("GET", "/api/runs/main-tk01-develop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var detail apiRunDetail
	require.NoError(t, json.NewDecoder(w.Body).Decode(&detail))
	require.Len(t, detail.TokenUsage, 1)
	assert.Equal(t, "claude", detail.TokenUsage[0].AgentName)
	assert.Equal(t, int64(140), detail.TokenUsage[0].InputTokens)
	assert.Equal(t, int64(60), detail.TokenUsage[0].OutputTokens)
}

func TestAPIRunDetail_PromptFileAndGitRevision(t *testing.T) {
	h, store := setupHandler(t)
	ctx := context.Background()
	dir := t.TempDir()

	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".cloche"), 0o755))
	wfText := `workflow develop {
    step implement {
        prompt = file(".cloche/prompts/implement.md")
        results = [success, fail]
    }
    implement:success -> done
    implement:fail -> abort
}`
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".cloche", "develop.cloche"), []byte(wfText), 0o644))

	run := domain.NewRun("main-pf01-develop", "develop")
	run.ProjectDir = dir
	run.BaseSHA = "0123456789abcdef"
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	req := httptest.NewRequest("GET", "/api/runs/main-pf01-develop", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var detail apiRunDetail
	require.NoError(t, json.NewDecoder(w.Body).Decode(&detail))
	assert.Equal(t, ".cloche/prompts/implement.md", detail.PromptFile)
	assert.Equal(t, "0123456", detail.GitRevision)
}
