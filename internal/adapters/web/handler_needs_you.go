package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/cloche-dev/cloche/internal/host"
)

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
