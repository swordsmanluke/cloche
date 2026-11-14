package web

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/swordsmanluke/cloche/internal/dra"
)

// DRAWaitService is the daemon's wait registry as the DRA API sees it. Every
// call is scoped to the DRA named by the caller's credential, so a DRA can
// only register, list and cancel its own waits.
type DRAWaitService interface {
	RegisterDRAWait(ctx context.Context, project, name string, req dra.WaitRequest) (string, error)
	ListDRAWaits(ctx context.Context, project, name string) ([]dra.Wait, error)
	CancelDRAWait(ctx context.Context, project, name, id string) error
}

// WithDRAWaits enables the DRA API's wait routes.
func WithDRAWaits(s DRAWaitService) HandlerOption {
	return func(h *Handler) { h.draWaits = s }
}

func (h *Handler) draWaitsReady(w http.ResponseWriter) bool {
	if h.draWaits == nil {
		draError(w, http.StatusNotFound, "DRA waits are not enabled")
		return false
	}
	return true
}

func (h *Handler) draRegisterWait(w http.ResponseWriter, r *http.Request) {
	c, ok := h.draClaims(w, r)
	if !ok || !h.draWaitsReady(w) {
		return
	}
	var req dra.WaitRequest
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		draError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	id, err := h.draWaits.RegisterDRAWait(r.Context(), c.Project, c.DRA, req)
	if err != nil {
		draError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]string{"id": id})
}

func (h *Handler) draListWaits(w http.ResponseWriter, r *http.Request) {
	c, ok := h.draClaims(w, r)
	if !ok || !h.draWaitsReady(w) {
		return
	}
	waits, err := h.draWaits.ListDRAWaits(r.Context(), c.Project, c.DRA)
	if err != nil {
		draError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if waits == nil {
		waits = []dra.Wait{}
	}
	writeJSONStatus(w, http.StatusOK, waits)
}

func (h *Handler) draCancelWait(w http.ResponseWriter, r *http.Request) {
	c, ok := h.draClaims(w, r)
	if !ok || !h.draWaitsReady(w) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		draError(w, http.StatusBadRequest, "id is required")
		return
	}
	if err := h.draWaits.CancelDRAWait(r.Context(), c.Project, c.DRA, body.ID); err != nil {
		draError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]string{"status": "ok"})
}

// draQuestions serves the questions routes to a DRA: the same module slots as
// the project routes, authenticated by the DRA credential instead of the
// project's signal token. A DRA may ask, await and read; the guide stays out.
func (h *Handler) draQuestions(next func(http.ResponseWriter, *http.Request, projectAuth)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		next(w, r, func(w http.ResponseWriter, r *http.Request) (string, bool) {
			c, ok := h.draClaims(w, r)
			return c.Project, ok
		})
	}
}
