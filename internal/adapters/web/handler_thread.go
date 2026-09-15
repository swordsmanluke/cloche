package web

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// ThreadSummary is a help thread's identifying metadata, mirroring
// pb.HelpThreadSummary (see internal/adapters/grpc's GetThread RPC) without
// pulling a clochepb dependency into this package.
type ThreadSummary struct {
	Address   string `json:"address"`
	Title     string `json:"title"`
	State     string `json:"state"`
	TaskID    string `json:"task_id,omitempty"`
	RunID     string `json:"run_id,omitempty"`
	StepName  string `json:"step_name,omitempty"`
	CreatedAt string `json:"created_at"`
}

// ThreadMessage is a single message in a help thread's transcript, mirroring
// pb.HelpMessageEntry.
type ThreadMessage struct {
	Author    string   `json:"author"` // "agent" | "user"
	Body      string   `json:"body"`
	Options   []string `json:"options,omitempty"`
	CreatedAt string   `json:"created_at"`
}

// GetThreadFunc resolves a help-thread address (or bare thread ID) to its
// summary and full message transcript.
type GetThreadFunc func(ctx context.Context, address string) (ThreadSummary, []ThreadMessage, error)

// ReplyThreadFunc appends a reply to a help thread. Implementations must
// follow the same path as `cloche threads reply`, including resuming the
// run if it is parked awaiting this thread.
type ReplyThreadFunc func(ctx context.Context, address, body string) error

// apiThreadDetail is the response for GET /api/runs/{id}/thread.
type apiThreadDetail struct {
	ThreadSummary
	Messages []ThreadMessage `json:"messages"`
}

// handleAPIRunThread returns the help thread a parked run is awaiting a
// reply on: its transcript backs the parked-run pane in the console.
func (h *Handler) handleAPIRunThread(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if run.ParkedThreadID == "" {
		http.Error(w, "run is not parked on a help thread", http.StatusNotFound)
		return
	}
	if h.getThreadFn == nil {
		http.Error(w, "help channel is not enabled on this daemon", http.StatusNotImplemented)
		return
	}

	summary, msgs, err := h.getThreadFn(r.Context(), run.ParkedThreadID)
	if err != nil {
		http.Error(w, "thread not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(apiThreadDetail{ThreadSummary: summary, Messages: msgs})
}

// handleAPIRunThreadReply posts a reply to the help thread a parked run is
// awaiting, resuming the run — the web equivalent of `cloche threads reply`.
func (h *Handler) handleAPIRunThreadReply(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if run.ParkedThreadID == "" {
		http.Error(w, "run is not parked on a help thread", http.StatusNotFound)
		return
	}
	if h.replyThreadFn == nil {
		http.Error(w, "help channel is not enabled on this daemon", http.StatusNotImplemented)
		return
	}

	var body struct {
		Body string `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Body) == "" {
		http.Error(w, "body is required", http.StatusBadRequest)
		return
	}

	if err := h.replyThreadFn(r.Context(), run.ParkedThreadID, body.Body); err != nil {
		http.Error(w, "failed to send reply", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
