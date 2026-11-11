package web

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/swordsmanluke/cloche/internal/signal"
)

// apiTrackerPhaseRequest is the body for POST .../tasks/create and
// .../tasks/update: the phase's contract inputs as string key/values.
type apiTrackerPhaseRequest struct {
	Inputs map[string]string `json:"inputs"`
}

// apiTrackerPhaseResponse carries the phase's outputs (new_task_id, found_task_id, task_found).
type apiTrackerPhaseResponse struct {
	Outputs map[string]string `json:"outputs"`
}

// handleAPITrackerPhase returns a handler that runs the create-task or
// update-task tracker phase through the project's bound tracker module. It
// needs the project's signal token, like POST .../signals: a created task is
// work the loop will run and merge.
func (h *Handler) handleAPITrackerPhase(slot string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dir, ok := h.signalAuth(w, r)
		if !ok {
			return
		}
		writeErr := func(code int, msg string) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"error": msg})
		}
		runner, _ := h.taskProvider.(TrackerPhaseRunner)
		if runner == nil {
			writeErr(http.StatusBadRequest, "task provider not configured")
			return
		}
		var req apiTrackerPhaseRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeErr(http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
			return
		}
		if src := req.Inputs["signal_source"]; src != "" {
			if err := signal.ValidateSource(src); err != nil {
				writeErr(http.StatusBadRequest, fmt.Sprintf("invalid signal_source: %v", err))
				return
			}
		}
		out, err := runner.RunTrackerPhase(r.Context(), dir, slot, req.Inputs)
		if err != nil {
			writeErr(http.StatusInternalServerError, fmt.Sprintf("%s failed: %v", slot, err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(apiTrackerPhaseResponse{Outputs: out})
	}
}
