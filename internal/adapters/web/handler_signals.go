package web

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"

	"github.com/swordsmanluke/cloche/internal/signal"
)

// lookupProjectDir resolves the {name} path value to a project directory
// without writing a response. The system project has no directory and is not
// a valid signal target.
func (h *Handler) lookupProjectDir(r *http.Request) (string, bool) {
	name := r.PathValue("name")
	projects, _ := h.store.ListProjects(r.Context())
	slugs := projectSlugs(projects)
	for dir, slug := range slugs {
		if slug == name || dir == name {
			return dir, dir != ""
		}
	}
	for dir, label := range legacyProjectLabels(projects) {
		if label == name {
			return dir, dir != ""
		}
	}
	return "", false
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// signalAuth resolves the project and checks its bearer token, writing a 401
// when either fails. Everything that can put work in the tracker goes through
// it: the dashboard listener may be reachable from the network.
func (h *Handler) signalAuth(w http.ResponseWriter, r *http.Request) (string, bool) {
	dir, found := h.lookupProjectDir(r)
	// Check the token even for an unknown project so both failures cost the same.
	valid := h.signalTokens != nil && h.signalTokens.Valid(dir, bearerToken(r))
	if !found || !valid {
		w.Header().Set("WWW-Authenticate", `Bearer realm="cloche signals"`)
		writeJSONStatus(w, http.StatusUnauthorized, map[string]string{"error": "missing or invalid bearer token"})
		return "", false
	}
	return dir, true
}

// handleAPISignals implements POST /api/projects/{name}/signals: the one way
// a feedback signal enters cloche. It needs the project's bearer token (an
// unknown project and a wrong token look identical, so project names can't be
// probed), validates every field, stores the signal and hands it to the
// handle-notification workflow.
func (h *Handler) handleAPISignals(w http.ResponseWriter, r *http.Request) {
	if h.signalTokens == nil || h.signalDispatcher == nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": "signal intake is not enabled"})
		return
	}
	dir, ok := h.signalAuth(w, r)
	if !ok {
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, signal.MaxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	var sig signal.Signal
	if err := dec.Decode(&sig); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSONStatus(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body too large"})
			return
		}
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body: " + err.Error()})
		return
	}
	if _, err := dec.Token(); err != io.EOF {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body: unexpected data after the signal object"})
		return
	}
	if err := sig.Validate(); err != nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	res, err := h.signalDispatcher.Submit(r.Context(), dir, sig)
	if errors.Is(err, signal.ErrLimited) {
		writeJSONStatus(w, http.StatusTooManyRequests, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		log.Printf("signals: storing signal for %s: %v", dir, err)
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": "could not store signal"})
		return
	}
	writeJSONStatus(w, http.StatusAccepted, map[string]any{"id": res.ID, "coalesced": res.Coalesced})
}
