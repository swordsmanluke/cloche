package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/host"
)

// QAGuideProvider serves the bound qa module's asking.md / judge.md. A
// TaskProvider that also implements it (and TrackerPhaseRunner) enables the
// questions routes.
type QAGuideProvider interface {
	QAGuide(projectDir string, judge bool) (string, error)
}

// maxQuestionsBody bounds a questions request body.
const maxQuestionsBody = 1 << 20

// splitIDs splits a module's whitespace/comma-separated id list.
func splitIDs(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == '\r' })
}

// projectAuth authenticates a request and names its project directory; it has
// already written the response when it fails.
type projectAuth func(w http.ResponseWriter, r *http.Request) (string, bool)

// questionsRoute authenticates with auth (the project's signal token, or a
// DRA credential), decodes the
// JSON body into req and runs the qa slot; it returns the slot's outputs.
// On failure it has already written the response.
func (h *Handler) questionsRoute(w http.ResponseWriter, r *http.Request, auth projectAuth, slot string, req any, inputs func() (map[string]string, error)) (map[string]string, bool) {
	dir, ok := auth(w, r)
	if !ok {
		return nil, false
	}
	fail := func(code int, msg string) (map[string]string, bool) {
		writeJSONStatus(w, code, map[string]string{"error": msg})
		return nil, false
	}
	runner, _ := h.taskProvider.(TrackerPhaseRunner)
	if runner == nil {
		return fail(http.StatusBadRequest, "task provider not configured")
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxQuestionsBody)
	if err := json.NewDecoder(r.Body).Decode(req); err != nil {
		return fail(http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err))
	}
	in, err := inputs()
	if err != nil {
		return fail(http.StatusBadRequest, err.Error())
	}
	out, err := runner.RunTrackerPhase(r.Context(), dir, slot, in)
	switch {
	case errors.Is(err, host.ErrNoQABind):
		return fail(http.StatusConflict, err.Error())
	case errors.Is(err, host.ErrPhaseFailed):
		return fail(http.StatusNotFound, fmt.Sprintf("%s: %v", slot, err))
	case err != nil:
		return fail(http.StatusInternalServerError, fmt.Sprintf("%s failed: %v", slot, err))
	}
	return out, true
}

// handleAPIQuestionsAsk implements POST .../questions/ask. Body:
// {"file": <question file as JSON>}. The daemon writes it to a temp file and
// hands the path to the module's ask slot as kv questions_file.
func (h *Handler) handleAPIQuestionsAsk(w http.ResponseWriter, r *http.Request) {
	h.questionsAsk(w, r, h.signalAuth)
}

func (h *Handler) questionsAsk(w http.ResponseWriter, r *http.Request, auth projectAuth) {
	var req struct {
		File    json.RawMessage `json:"file"`
		NoJudge bool            `json:"no_judge"`
	}
	var tmp string
	defer func() {
		if tmp != "" {
			os.Remove(tmp)
		}
	}()
	out, ok := h.questionsRoute(w, r, auth, host.SlotAsk, &req, func() (map[string]string, error) {
		if len(req.File) == 0 {
			return nil, errors.New("missing file")
		}
		f, err := os.CreateTemp("", "cloche-questions-*.json")
		if err != nil {
			return nil, err
		}
		tmp = f.Name()
		_, werr := f.Write(req.File)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return nil, werr
		}
		return map[string]string{"questions_file": tmp}, nil
	})
	if !ok {
		return
	}
	ids := splitIDs(out["question_ids"])
	if dir, found := h.lookupProjectDir(r); found {
		h.maybeLaunchJudge(r.Context(), dir, ids, req.NoJudge)
	}
	writeJSONStatus(w, http.StatusOK, map[string][]string{"ids": ids})
}

// maybeLaunchJudge asks the JudgeLauncher to judge ids once, unless the ask
// opted out (--no-judge) or the project set [questions] judge = false. A
// launcher error is logged and never fails the ask.
func (h *Handler) maybeLaunchJudge(ctx context.Context, dir string, ids []string, noJudge bool) {
	if noJudge || len(ids) == 0 {
		return
	}
	cfg, err := config.Load(dir)
	if err != nil {
		// Unreadable config: fall back to the default (judge on) but say so.
		log.Printf("questions ask: loading config for %s: %v", dir, err)
	} else if !cfg.Questions.Judge {
		return
	}
	l := h.judgeLauncher
	if l == nil {
		return
	}
	if err := l.LaunchJudge(ctx, dir, ids); err != nil {
		log.Printf("questions ask: judge not launched for %s: %v", dir, err)
	}
}

// handleAPIQuestionsAwait implements POST .../questions/await (non-blocking).
// Body: {"ids": [...]}. Response: {"open": [...]}.
func (h *Handler) handleAPIQuestionsAwait(w http.ResponseWriter, r *http.Request) {
	h.questionsAwait(w, r, h.signalAuth)
}

func (h *Handler) questionsAwait(w http.ResponseWriter, r *http.Request, auth projectAuth) {
	var req struct {
		IDs []string `json:"ids"`
	}
	out, ok := h.questionsRoute(w, r, auth, host.SlotAwaitAnswer, &req, func() (map[string]string, error) {
		if len(req.IDs) == 0 {
			return nil, errors.New("missing ids")
		}
		return map[string]string{"question_ids": strings.Join(req.IDs, "\n")}, nil
	})
	if !ok {
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string][]string{"open": splitIDs(out["questions_open"])})
}

// handleAPIQuestionsRead implements POST .../questions/read. Body:
// {"id": "..."}. Response: {"answer": "<text>"}; 404 when there is no answer.
func (h *Handler) handleAPIQuestionsRead(w http.ResponseWriter, r *http.Request) {
	h.questionsRead(w, r, h.signalAuth)
}

func (h *Handler) questionsRead(w http.ResponseWriter, r *http.Request, auth projectAuth) {
	var req struct {
		ID string `json:"id"`
	}
	out, ok := h.questionsRoute(w, r, auth, host.SlotReadAnswer, &req, func() (map[string]string, error) {
		if strings.TrimSpace(req.ID) == "" {
			return nil, errors.New("missing id")
		}
		return map[string]string{"question_id": req.ID}, nil
	})
	if !ok {
		return
	}
	data, err := os.ReadFile(out["answer_path"])
	if err != nil {
		writeJSONStatus(w, http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("reading answer_path: %v", err)})
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]string{"answer": string(data)})
}

// handleAPIQuestionsGuide implements GET .../questions/guide[?judge=1]: the
// bound module's asking.md or judge.md as text/plain.
func (h *Handler) handleAPIQuestionsGuide(w http.ResponseWriter, r *http.Request) {
	dir, ok := h.signalAuth(w, r)
	if !ok {
		return
	}
	gp, _ := h.taskProvider.(QAGuideProvider)
	if gp == nil {
		writeJSONStatus(w, http.StatusBadRequest, map[string]string{"error": "task provider not configured"})
		return
	}
	text, err := gp.QAGuide(dir, r.URL.Query().Get("judge") != "")
	if errors.Is(err, host.ErrNoQABind) {
		writeJSONStatus(w, http.StatusConflict, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		writeJSONStatus(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(text))
}
