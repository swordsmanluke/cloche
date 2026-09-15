package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"sort"
	"strings"

	"github.com/swordsmanluke/cloche/internal/domain"
)

// apiAttempt is one attempt's summary for the console's attempt-tab facts row.
type apiAttempt struct {
	AttemptNum  int    `json:"attempt_num"`
	AttemptID   string `json:"attempt_id,omitempty"`
	RunID       string `json:"run_id"`
	Outcome     string `json:"outcome"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at,omitempty"`
	Duration    string `json:"duration"`
	// RetryReason names the step where the *previous* attempt failed, empty
	// for the first attempt.
	RetryReason string `json:"retry_reason,omitempty"`
	// FailedStep names the step where *this* attempt itself failed, empty if
	// it didn't fail. Used by the needs-you compare view to fetch each
	// attempt's failing-step log (see handleAPIStepOutput).
	FailedStep string `json:"failed_step,omitempty"`
}

// apiTaskAttempts is the response for GET /api/projects/{name}/tasks/{taskId}/attempts.
type apiTaskAttempts struct {
	TaskID   string       `json:"task_id"`
	Title    string       `json:"title,omitempty"`
	Status   string       `json:"status"`
	Attempts []apiAttempt `json:"attempts"` // oldest first
}

// handleAPITaskAttempts returns the ordered (oldest-first) list of attempts
// for a task, one entry per top-level run. The retry reason on attempt N
// (N>1) names the step where attempt N-1 failed, reusing the flattened step
// tree (see flattenRun) so a failure inside a spawned child run is found too.
func (h *Handler) handleAPITaskAttempts(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	taskID := r.PathValue("taskId")

	runs, err := h.store.ListRunsFiltered(r.Context(), domain.RunListFilter{ProjectDir: dir, TaskID: taskID})
	if err != nil || len(runs) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "task not found"})
		return
	}

	byID := map[string]*domain.Run{}
	for _, rr := range runs {
		byID[rr.ID] = rr
	}
	parentMap := map[string][]*domain.Run{}
	var topLevel []*domain.Run
	for _, rr := range runs {
		if rr.ParentRunID != "" && byID[rr.ParentRunID] != nil {
			parentMap[rr.ParentRunID] = append(parentMap[rr.ParentRunID], rr)
		} else {
			topLevel = append(topLevel, rr)
		}
	}
	sort.SliceStable(topLevel, func(i, j int) bool {
		return topLevel[i].StartedAt.Before(topLevel[j].StartedAt)
	})

	var attempts []apiAttempt
	var prevFailedStep string
	for i, tr := range topLevel {
		allInAttempt := append([]*domain.Run{tr}, parentMap[tr.ID]...)
		status := taskAggregateStatus(allInAttempt)
		failedStep := h.firstFailedStepLabel(r.Context(), tr.ID)

		attempts = append(attempts, apiAttempt{
			AttemptNum:  i + 1,
			AttemptID:   tr.AttemptID,
			RunID:       tr.ID,
			Outcome:     status,
			StartedAt:   formatTime(tr.StartedAt),
			CompletedAt: formatTime(tr.CompletedAt),
			Duration:    formatRunTiming(tr.State, tr.StartedAt, tr.CompletedAt),
			RetryReason: prevFailedStep,
			FailedStep:  failedStep,
		})
		prevFailedStep = failedStep
	}

	taskTitles := h.taskTitlesFromRuns(runs)
	status := ""
	if len(attempts) > 0 {
		status = attempts[len(attempts)-1].Outcome
	}

	resp := apiTaskAttempts{
		TaskID:   taskID,
		Title:    taskTitles[taskID],
		Status:   status,
		Attempts: attempts,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// firstFailedStepLabel finds the first failed step in runID's flattened step
// tree (host run + inlined child-run steps), returning its name, or "" if
// the run has no failed step.
func (h *Handler) firstFailedStepLabel(ctx context.Context, runID string) string {
	steps, err := h.flattenRun(ctx, runID, 0, -1)
	if err != nil {
		return ""
	}
	for _, s := range steps {
		if s.Result == "fail" || s.Result == "error" {
			return s.StepName
		}
	}
	return ""
}

// apiRepoBranch is one repo's extracted result branch.
type apiRepoBranch struct {
	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch"`
}

// handleAPIRunBranch returns the git branch(es) a run's results were
// extracted to, read from the daemon's context KV store (see
// writeRepoBranchKV in the grpc executor). Empty when no extraction has
// happened yet — e.g. the run is still running, or failed before extraction.
// Backs the header's "Open branch" action on completed tasks.
func (h *Handler) handleAPIRunBranch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	branches := h.resultBranches(r.Context(), run)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"branches": branches})
}

// resultBranches looks up the branch(es) a run's results were extracted to
// from the context KV store, preferring the per-repo child_repos/child_branch:<name>
// keys and falling back to the single-repo child_branch key.
func (h *Handler) resultBranches(ctx context.Context, run *domain.Run) []apiRepoBranch {
	if run.TaskID == "" {
		return nil
	}
	var branches []apiRepoBranch
	if names, ok, _ := h.store.GetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, "child_repos"); ok && names != "" {
		for _, name := range strings.Split(names, ",") {
			if branch, ok, _ := h.store.GetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, "child_branch:"+name); ok && branch != "" {
				branches = append(branches, apiRepoBranch{Repo: name, Branch: branch})
			}
		}
		return branches
	}
	if branch, ok, _ := h.store.GetContextKey(ctx, run.TaskID, run.AttemptID, run.ID, "child_branch"); ok && branch != "" {
		branches = append(branches, apiRepoBranch{Branch: branch})
	}
	return branches
}

// handleAPIRunDiff returns the diff of a run's extracted result branch
// against the base commit it started from (run.BaseSHA), for the header's
// "Diff" action on completed tasks. The branch is always resolved
// server-side from the KV store (never from client input) so the git ref
// passed to the diff command can't be attacker-controlled.
func (h *Handler) handleAPIRunDiff(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if run.BaseSHA == "" {
		http.Error(w, "no base revision recorded for this run", http.StatusNotFound)
		return
	}

	branches := h.resultBranches(r.Context(), run)
	if len(branches) == 0 {
		http.Error(w, "no result branch recorded for this run", http.StatusNotFound)
		return
	}

	cmd := exec.Command("git", "diff", run.BaseSHA+".."+branches[0].Branch)
	cmd.Dir = run.ProjectDir
	out, err := cmd.Output()
	if err != nil {
		http.Error(w, "diff not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
}

// handleAPIRunConsole returns the raw (unparsed) container log for a run —
// the literal stdout/stderr stream, as opposed to the structured, per-line
// "Log" pane fed by full.log. Backs the header's "Console" action on
// running tasks.
func (h *Handler) handleAPIRunConsole(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if h.container == nil || run.ContainerID == "" {
		http.Error(w, "no container available for this run", http.StatusNotFound)
		return
	}
	logs, err := h.container.Logs(r.Context(), run.ContainerID)
	if err != nil {
		http.Error(w, "failed to fetch container logs", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(logs))
}
