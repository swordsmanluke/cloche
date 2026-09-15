package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/swordsmanluke/cloche/internal/host"
)

// apiAttentionItem mirrors attention.Item for the GET
// .../attention response, with Since rendered as an RFC3339 string.
type apiAttentionItem struct {
	Kind          string   `json:"kind"`
	ProjectDir    string   `json:"project_dir"`
	TaskID        string   `json:"task_id,omitempty"`
	RunID         string   `json:"run_id,omitempty"`
	Reason        string   `json:"reason"`
	Since         string   `json:"since"`
	Actions       []string `json:"actions,omitempty"`
	ThreadAddress string   `json:"thread_address,omitempty"`
	Key           string   `json:"key,omitempty"`
}

// apiAttentionResponse is the body for GET /api/projects/{name}/attention.
// ComputedAt is empty when the project has never been refreshed by the
// attention cache (see internal/attention.Cache) — the dashboard should show
// this as "not yet computed" rather than treating it as stale data.
type apiAttentionResponse struct {
	Items      []apiAttentionItem `json:"items"`
	ComputedAt string             `json:"computed_at"`
}

// handleAPIProjectAttention returns the cached "Needs you" attention set for
// a single project, along with when it was last computed. Always reads from
// the attention cache — never runs the project's tracker on this request.
func (h *Handler) handleAPIProjectAttention(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	resp := apiAttentionResponse{Items: []apiAttentionItem{}}
	if h.attentionProvider != nil {
		snap := h.attentionProvider.AttentionSnapshot(dir)
		resp.ComputedAt = apiTimeString(snap.ComputedAt)
		for _, item := range snap.Items {
			resp.Items = append(resp.Items, apiAttentionItem{
				Kind:          string(item.Kind),
				ProjectDir:    item.ProjectDir,
				TaskID:        item.TaskID,
				RunID:         item.RunID,
				Reason:        item.Reason,
				Since:         apiTimeString(item.Since),
				Actions:       item.Actions,
				ThreadAddress: item.ThreadAddress,
				Key:           item.Key,
			})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAPICloseTask runs the project's close/cancel task contract for a
// task — the "close in tracker" needs-you action. Responds 501 with a hint
// (rather than 500) when the project defines neither a "close-task" nor
// "cancel-task" standalone host workflow, so the dashboard can show "not
// available" instead of treating it as a failure.
func (h *Handler) handleAPICloseTask(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	taskID := r.PathValue("taskId")
	if taskID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "task ID is required"})
		return
	}

	if h.taskProvider == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "task provider not configured"})
		return
	}

	if err := h.taskProvider.CloseTask(r.Context(), dir, taskID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		if errors.Is(err, host.ErrNoCloseContract) {
			w.WriteHeader(http.StatusNotImplemented)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "close in tracker isn't available for this project",
				"hint":  `define a standalone "close-task" or "cancel-task" host workflow to enable it`,
			})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to close task: %v", err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// apiRunOnceRequest is the body for POST .../tasks/{taskId}/run-once.
type apiRunOnceRequest struct {
	Workflow string `json:"workflow"`
	Prompt   string `json:"prompt,omitempty"`
}

// handleAPIRunOnce dispatches a single attempt of a named workflow for a
// task, outside the orchestration loop — the "run once" needs-you action.
func (h *Handler) handleAPIRunOnce(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	taskID := r.PathValue("taskId")
	if taskID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "task ID is required"})
		return
	}

	var req apiRunOnceRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Workflow == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "workflow is required"})
		return
	}

	if h.taskProvider == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "task provider not configured"})
		return
	}

	runID, err := h.taskProvider.RunOnce(r.Context(), dir, taskID, req.Workflow, req.Prompt)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to dispatch run: %v", err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "run_id": runID})
}

// apiMuteAttentionRequest is the body for POST .../attention/mute.
type apiMuteAttentionRequest struct {
	Key string `json:"key"`
}

// handleAPIMuteAttention mutes a "Needs you" item by its stable Key (see
// attention.Item.Key) — currently only meaningful for builtin-failures items.
func (h *Handler) handleAPIMuteAttention(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	var req apiMuteAttentionRequest
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&req)
	}
	if req.Key == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "key is required"})
		return
	}

	if h.attentionMuter == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "attention muter not configured"})
		return
	}

	if err := h.attentionMuter.MuteAttentionItem(r.Context(), dir, req.Key); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to mute: %v", err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
