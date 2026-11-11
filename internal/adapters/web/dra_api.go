package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/draauth"
	"github.com/swordsmanluke/cloche/internal/signal"
)

// The DRA API: the only part of the daemon a DRA container may call. A DRA
// presents the credential minted for its wake-up (draauth), which names its
// run, project and domain. With it a DRA may list its project's tasks and
// runs, read a run's status and output, create a task (always labelled with
// the DRA's domain), and update or close a task that carries that label.
// The rest of the dashboard has no auth yet (a later story), so this limits a
// DRA that uses the CLI it is given, not one that calls other routes directly.
//
// The project is always taken from the credential, never from the request, so
// a DRA cannot name another project.

// ReadyLabel marks a task the orchestration loop may pick up (beads-tasks).
// A DRA does not set it: handing a task to the loop spends budget, so that
// stays a decision for a person (open question recorded in docs/dras.md).
const ReadyLabel = "ready"

var draRoutePatterns = []string{
	"GET /api/dra/tasks",
	"GET /api/dra/runs",
	"GET /api/dra/runs/{id}",
	"GET /api/dra/runs/{id}/steps/{step}/output",
	"POST /api/dra/tasks/create",
	"POST /api/dra/tasks/update",
	"POST /api/dra/tasks/close",
}

// WithDRAAccess enables the DRA API. secret is the daemon secret DRA
// credentials are signed with (see docker.Runtime.SetMCPSecret).
func WithDRAAccess(secret []byte) HandlerOption {
	return func(h *Handler) { h.draSecret = secret }
}

// WithDRAActionHook registers fn to be told, after the DRA API creates,
// updates or closes a task for a DRA, which task it was. The daemon uses it
// so the events the DRA's own actions cause do not wake it again.
func WithDRAActionHook(fn func(project, dra, taskID string)) HandlerOption {
	return func(h *Handler) { h.draAction = fn }
}

func (h *Handler) draActed(c draauth.Claims, taskID string) {
	if h.draAction != nil && taskID != "" {
		h.draAction(c.Project, c.DRA, taskID)
	}
}

func (h *Handler) registerDRARoutes() {
	handlers := map[string]http.HandlerFunc{
		"GET /api/dra/tasks":                         h.draTasks,
		"GET /api/dra/runs":                          h.draRuns,
		"GET /api/dra/runs/{id}":                     h.draRun(h.handleAPIRunDetail),
		"GET /api/dra/runs/{id}/steps/{step}/output": h.draRun(h.handleAPIStepOutput),
		"POST /api/dra/tasks/create":                 h.draCreateTask,
		"POST /api/dra/tasks/update":                 h.draUpdateTask,
		"POST /api/dra/tasks/close":                  h.draCloseTask,
	}
	for _, p := range draRoutePatterns {
		h.route(p, handlers[p])
	}
}

// draClaims validates the credential and that its run is still alive,
// writing a 401 otherwise. A credential stops working the moment its run
// leaves an active state (finished, failed, cancelled or parked).
func (h *Handler) draClaims(w http.ResponseWriter, r *http.Request) (draauth.Claims, bool) {
	deny := func(msg string) (draauth.Claims, bool) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cloche dra"`)
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": msg})
		return draauth.Claims{}, false
	}
	c, ok := draauth.Parse(h.draSecret, bearerToken(r))
	if !ok {
		return deny("missing or invalid DRA credential")
	}
	run, err := h.store.GetRun(r.Context(), c.RunID)
	if err != nil || run == nil || run.ProjectDir != c.Project || !domain.IsActiveRunState(run.State) {
		return deny("DRA credential is no longer valid: its run has ended")
	}
	return c, true
}

func draError(w http.ResponseWriter, code int, msg string) {
	writeJSONStatus(w, code, map[string]string{"error": msg})
}

// draRun guards a run-scoped read: the run must belong to the DRA's project.
// An unknown run and another project's run look the same.
func (h *Handler) draRun(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, ok := h.draClaims(w, r)
		if !ok {
			return
		}
		run, err := h.store.GetRun(r.Context(), r.PathValue("id"))
		if err != nil || run == nil || run.ProjectDir != c.Project {
			http.Error(w, "run not found", http.StatusNotFound)
			return
		}
		next(w, r)
	}
}

func (h *Handler) draTasks(w http.ResponseWriter, r *http.Request) {
	c, ok := h.draClaims(w, r)
	if !ok {
		return
	}
	tasks := []TaskEntry{}
	if h.taskProvider != nil {
		if t := h.taskProvider.GetLoopTasks(c.Project); t != nil {
			tasks = t
		}
	}
	writeJSONStatus(w, http.StatusOK, tasks)
}

func (h *Handler) draRuns(w http.ResponseWriter, r *http.Request) {
	c, ok := h.draClaims(w, r)
	if !ok {
		return
	}
	runs, err := h.store.ListRunsByProject(r.Context(), c.Project, time.Time{})
	if err != nil {
		draError(w, http.StatusInternalServerError, "could not list runs")
		return
	}
	labels := map[string]string{}
	out := make([]apiRun, 0, len(runs))
	for _, run := range runs {
		out = append(out, toAPIRun(run, labels))
	}
	writeJSONStatus(w, http.StatusOK, out)
}

// trackerRunner returns the tracker-phase runner, or writes a 400.
func (h *Handler) draTrackerRunner(w http.ResponseWriter) TrackerPhaseRunner {
	runner, _ := h.taskProvider.(TrackerPhaseRunner)
	if runner == nil {
		draError(w, http.StatusBadRequest, "task provider not configured")
	}
	return runner
}

func splitLabels(s string) []string {
	var out []string
	for _, l := range strings.Split(s, ",") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

// draOwns reports whether taskID exists in the tracker and carries the DRA's
// domain label.
func (h *Handler) draOwns(ctx context.Context, runner TrackerPhaseRunner, c draauth.Claims, taskID string) (bool, error) {
	out, err := runner.RunTrackerPhase(ctx, c.Project, "update-task", map[string]string{
		"action": "get", "target_task_id": taskID,
	})
	if err != nil {
		return false, err
	}
	return out["task_found"] == "true" && hasLabel(splitLabels(out["task_labels"]), c.DRA), nil
}

func decodeInputs(r *http.Request) (map[string]string, error) {
	r.Body = http.MaxBytesReader(nil, r.Body, signal.MaxBodyBytes)
	var req apiTrackerPhaseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return nil, fmt.Errorf("invalid request body: %v", err)
	}
	if req.Inputs == nil {
		req.Inputs = map[string]string{}
	}
	return req.Inputs, nil
}

// onlyKeys returns an error naming any input outside allowed.
func onlyKeys(in map[string]string, allowed ...string) error {
	ok := map[string]bool{}
	for _, k := range allowed {
		ok[k] = true
	}
	for k := range in {
		if !ok[k] {
			return fmt.Errorf("input %q is not allowed for a DRA", k)
		}
	}
	return nil
}

func (h *Handler) draCreateTask(w http.ResponseWriter, r *http.Request) {
	c, ok := h.draClaims(w, r)
	if !ok {
		return
	}
	runner := h.draTrackerRunner(w)
	if runner == nil {
		return
	}
	in, err := decodeInputs(r)
	if err == nil {
		err = onlyKeys(in, "title", "body", "priority", "labels", "signal_source", "signal_key")
	}
	if err != nil {
		draError(w, http.StatusBadRequest, err.Error())
		return
	}
	if src := in["signal_source"]; src != "" {
		if err := signal.ValidateSource(src); err != nil {
			draError(w, http.StatusBadRequest, fmt.Sprintf("invalid signal_source: %v", err))
			return
		}
	}
	labels := splitLabels(in["labels"])
	if hasLabel(labels, ReadyLabel) {
		draError(w, http.StatusForbidden, "a DRA cannot mark a task 'ready'; a person promotes it to the loop")
		return
	}
	if !hasLabel(labels, c.DRA) {
		labels = append(labels, c.DRA)
	}
	in["labels"] = strings.Join(labels, ",")
	out, err := runner.RunTrackerPhase(r.Context(), c.Project, "create-task", in)
	if err != nil {
		draError(w, http.StatusInternalServerError, fmt.Sprintf("create-task failed: %v", err))
		return
	}
	h.draActed(c, out["new_task_id"])
	writeJSONStatus(w, http.StatusOK, apiTrackerPhaseResponse{Outputs: out})
}

func (h *Handler) draUpdateTask(w http.ResponseWriter, r *http.Request) {
	c, ok := h.draClaims(w, r)
	if !ok {
		return
	}
	runner := h.draTrackerRunner(w)
	if runner == nil {
		return
	}
	in, err := decodeInputs(r)
	if err == nil {
		err = onlyKeys(in, "action", "target_task_id", "comment", "priority", "add_labels", "remove_labels", "signal_source", "signal_key")
	}
	if err != nil {
		draError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch in["action"] {
	case "find":
		if err := onlyKeys(in, "action", "signal_source", "signal_key"); err != nil {
			draError(w, http.StatusBadRequest, err.Error())
			return
		}
		if src := in["signal_source"]; src != "" {
			if err := signal.ValidateSource(src); err != nil {
				draError(w, http.StatusBadRequest, fmt.Sprintf("invalid signal_source: %v", err))
				return
			}
		}
		out, err := runner.RunTrackerPhase(r.Context(), c.Project, "update-task", in)
		if err != nil {
			draError(w, http.StatusInternalServerError, fmt.Sprintf("update-task failed: %v", err))
			return
		}
		// A task of another domain is not visible to this DRA.
		if out["task_found"] == "true" {
			if own, err := h.draOwns(r.Context(), runner, c, out["found_task_id"]); err != nil || !own {
				out = map[string]string{"task_found": "false"}
			}
		}
		writeJSONStatus(w, http.StatusOK, apiTrackerPhaseResponse{Outputs: out})
		return
	case "update":
	default:
		draError(w, http.StatusBadRequest, `action must be "update" or "find"`)
		return
	}
	id := in["target_task_id"]
	if id == "" {
		draError(w, http.StatusBadRequest, "target_task_id is required")
		return
	}
	if hasLabel(splitLabels(in["add_labels"]), ReadyLabel) {
		draError(w, http.StatusForbidden, "a DRA cannot mark a task 'ready'; a person promotes it to the loop")
		return
	}
	if hasLabel(splitLabels(in["remove_labels"]), c.DRA) {
		draError(w, http.StatusForbidden, "a DRA cannot remove its own domain label from a task")
		return
	}
	if !h.draCheckOwner(w, r, runner, c, id) {
		return
	}
	out, err := runner.RunTrackerPhase(r.Context(), c.Project, "update-task", in)
	if err != nil {
		draError(w, http.StatusInternalServerError, fmt.Sprintf("update-task failed: %v", err))
		return
	}
	h.draActed(c, id)
	writeJSONStatus(w, http.StatusOK, apiTrackerPhaseResponse{Outputs: out})
}

// draCheckOwner refuses (403) a task that is missing or lacks the DRA's label.
func (h *Handler) draCheckOwner(w http.ResponseWriter, r *http.Request, runner TrackerPhaseRunner, c draauth.Claims, id string) bool {
	own, err := h.draOwns(r.Context(), runner, c, id)
	if err != nil {
		draError(w, http.StatusInternalServerError, fmt.Sprintf("checking task %s: %v", id, err))
		return false
	}
	if !own {
		draError(w, http.StatusForbidden, fmt.Sprintf("task %s does not carry this DRA's domain label %q", id, c.DRA))
		return false
	}
	return true
}

func (h *Handler) draCloseTask(w http.ResponseWriter, r *http.Request) {
	c, ok := h.draClaims(w, r)
	if !ok {
		return
	}
	runner := h.draTrackerRunner(w)
	if runner == nil {
		return
	}
	var body struct {
		TaskID string `json:"task_id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TaskID == "" {
		draError(w, http.StatusBadRequest, "task_id is required")
		return
	}
	if !h.draCheckOwner(w, r, runner, c, body.TaskID) {
		return
	}
	h.draActed(c, body.TaskID) // before closing: the close itself must not wake the DRA
	if err := h.taskProvider.CloseTask(r.Context(), c.Project, body.TaskID); err != nil {
		draError(w, http.StatusInternalServerError, fmt.Sprintf("failed to close task: %v", err))
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]string{"status": "ok"})
}
