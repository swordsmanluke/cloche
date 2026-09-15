package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/activitylog"
)

// Page size bounds for the activity stream API. The stream is always a
// bounded tail, never the full activity_log table: the first page is capped
// to today plus activityDefaultLimit rows, and older history is paged in via
// the opaque "before" cursor (an activity_log row ID).
const (
	activityDefaultLimit = 50
	activityMaxLimit     = 200
)

// ActivityLine is one rendered line of the activity ticker/stream, either a
// single activity_log entry or several repeats of the same signature
// collapsed together (Count > 1; see groupActivityEntries).
type ActivityLine struct {
	ID           int64  `json:"id"`
	Timestamp    string `json:"ts"`
	ProjectDir   string `json:"project_dir,omitempty"`
	ProjectLabel string `json:"project_label,omitempty"`
	Kind         string `json:"kind"`
	WorkflowName string `json:"workflow,omitempty"`
	StepName     string `json:"step,omitempty"`
	Result       string `json:"result,omitempty"`
	State        string `json:"state,omitempty"`
	Message      string `json:"message,omitempty"`
	Text         string `json:"text"`
	Failure      bool   `json:"failure"`
	Count        int    `json:"count,omitempty"`

	signature string
	day       string
}

// activityStreamResponse is the payload for GET /api/activity.
type activityStreamResponse struct {
	Entries []ActivityLine `json:"entries"`
	Cursor  string         `json:"cursor,omitempty"`
}

// handleAPIActivity serves the activity ticker/stream: GET /api/activity,
// optionally filtered by ?project=<slug> (all projects when omitted) and
// ?failures_only=1, paged via ?before=<cursor>&limit=<n>. The response is
// always a bounded tail with repeated same-signature entries collapsed into
// one line with a count (see groupActivityEntries), never the full table.
func (h *Handler) handleAPIActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/json")

	projectDir := ""
	if slug := q.Get("project"); slug != "" {
		dir, ok := h.projectDirForSlug(r, slug)
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			json.NewEncoder(w).Encode(map[string]string{"error": "project not found"})
			return
		}
		projectDir = dir
	}

	if h.activityStore == nil {
		json.NewEncoder(w).Encode(activityStreamResponse{Entries: []ActivityLine{}})
		return
	}

	limit := activityDefaultLimit
	if raw := q.Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > activityMaxLimit {
		limit = activityMaxLimit
	}

	var beforeID int64
	if raw := q.Get("before"); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			beforeID = n
		}
	}
	failuresOnly := q.Get("failures_only") == "1"

	now := time.Now()
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	opts := activitylog.ReadOptions{
		BeforeID:     beforeID,
		Limit:        limit,
		FailuresOnly: failuresOnly,
	}
	if beforeID == 0 {
		opts.Since = startOfToday
	}

	entries, err := h.activityStore.ReadActivityEntries(r.Context(), projectDir, opts)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to read activity log: %v", err)})
		return
	}

	labels := map[string]string{}
	if projectDir == "" {
		dirs, _ := h.store.ListProjects(r.Context())
		labels = projectLabels(dirs)
	} else {
		labels[projectDir] = projectLabels([]string{projectDir})[projectDir]
	}

	resp := activityStreamResponse{Entries: groupActivityEntries(entries, labels, now)}

	switch {
	case len(entries) == 0:
		// Nothing today (or in this page); still worth checking for a
		// "load earlier" cursor when we only looked at today's window.
		if beforeID == 0 {
			resp.Cursor = h.earliestOlderCursor(r, projectDir, startOfToday, failuresOnly)
		}
	case len(entries) >= limit:
		resp.Cursor = strconv.FormatInt(entries[0].ID, 10)
	case beforeID == 0:
		resp.Cursor = h.earliestOlderCursor(r, projectDir, startOfToday, failuresOnly)
	}

	json.NewEncoder(w).Encode(resp)
}

// earliestOlderCursor checks (with a single bounded row read) whether any
// activity predates before, returning a cursor to it if so, or "" if the
// stream is exhausted.
func (h *Handler) earliestOlderCursor(r *http.Request, projectDir string, before time.Time, failuresOnly bool) string {
	older, err := h.activityStore.ReadActivityEntries(r.Context(), projectDir, activitylog.ReadOptions{
		Until:        before.Add(-time.Nanosecond),
		Limit:        1,
		FailuresOnly: failuresOnly,
	})
	if err != nil || len(older) == 0 {
		return ""
	}
	return strconv.FormatInt(older[0].ID, 10)
}

// projectDirForSlug resolves a project slug to its directory without the
// resolveProjectDir helper's "name" path-value assumption (the activity
// endpoint takes it as a query parameter instead, since "all projects" is a
// valid selection too).
func (h *Handler) projectDirForSlug(r *http.Request, slug string) (string, bool) {
	projects, _ := h.store.ListProjects(r.Context())
	slugs := projectSlugs(projects)
	for dir, s := range slugs {
		if s == slug {
			return dir, true
		}
	}
	return "", false
}

// groupActivityEntries formats entries (already sorted oldest-first, as
// ReadActivityEntries returns them) into display lines, collapsing runs of
// consecutive entries sharing the same signature (project, kind, workflow,
// step, result, state) on the same calendar day into a single line whose
// text carries an ordinal count (e.g. "intent-scan failed · 8th today").
// Lines are returned newest-first, matching the tail/ticker convention.
func groupActivityEntries(entries []activitylog.Entry, labels map[string]string, now time.Time) []ActivityLine {
	if len(entries) == 0 {
		return []ActivityLine{}
	}
	today := now.Format("2006-01-02")

	lines := make([]ActivityLine, 0, len(entries))
	for _, e := range entries {
		sig := activitySignature(e)
		day := e.Timestamp.Format("2006-01-02")
		text := formatActivityText(e)

		if n := len(lines); n > 0 && lines[n-1].signature == sig && lines[n-1].day == day {
			last := &lines[n-1]
			last.Count++
			last.ID = e.ID
			last.Timestamp = apiTimeString(e.Timestamp)
			last.Text = decorateWithOrdinal(text, last.Count, day, today)
			continue
		}

		lines = append(lines, ActivityLine{
			ID:           e.ID,
			Timestamp:    apiTimeString(e.Timestamp),
			ProjectDir:   e.ProjectDir,
			ProjectLabel: labels[e.ProjectDir],
			Kind:         string(e.Kind),
			WorkflowName: e.WorkflowName,
			StepName:     e.StepName,
			Result:       e.Result,
			State:        e.State,
			Message:      e.Message,
			Text:         text,
			Failure:      e.IsFailure(),
			Count:        1,
			signature:    sig,
			day:          day,
		})
	}

	for i, j := 0, len(lines)-1; i < j; i, j = i+1, j-1 {
		lines[i], lines[j] = lines[j], lines[i]
	}
	return lines
}

// activitySignature identifies entries that should collapse together when
// repeated: same project, same event shape, same outcome.
func activitySignature(e activitylog.Entry) string {
	return strings.Join([]string{
		e.ProjectDir, string(e.Kind), e.WorkflowName, e.StepName, e.Result, e.State,
	}, "\x1f")
}

// decorateWithOrdinal appends an ordinal-count suffix to text once a
// signature has repeated more than once on a given day (e.g. "· 8th today").
// A day other than today is spelled out (e.g. "· 3rd 2026-09-12") since
// "today" would be misleading once the stream scrolls into prior days.
func decorateWithOrdinal(text string, count int, day, today string) string {
	if count <= 1 {
		return text
	}
	dayLabel := day
	if day == today {
		dayLabel = "today"
	}
	return fmt.Sprintf("%s · %s %s", text, ordinal(count), dayLabel)
}

func ordinal(n int) string {
	if n%100 >= 11 && n%100 <= 13 {
		return fmt.Sprintf("%dth", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("%dst", n)
	case 2:
		return fmt.Sprintf("%dnd", n)
	case 3:
		return fmt.Sprintf("%drd", n)
	default:
		return fmt.Sprintf("%dth", n)
	}
}

// formatActivityText renders a human-readable one-liner for a single
// activity_log entry, before any repeat-collapsing decoration is applied.
func formatActivityText(e activitylog.Entry) string {
	wf := e.WorkflowName
	if wf == "" {
		wf = "run"
	}
	switch e.Kind {
	case activitylog.KindAttemptStarted:
		return wf + " started"
	case activitylog.KindAttemptEnded:
		switch e.State {
		case "":
			return wf + " ended"
		default:
			return wf + " " + e.State
		}
	case activitylog.KindStepStarted:
		return wf + ": " + e.StepName + " started"
	case activitylog.KindStepCompleted:
		switch e.Result {
		case "fail":
			return wf + ": " + e.StepName + " failed"
		case "success":
			return wf + ": " + e.StepName + " done"
		default:
			return wf + ": " + e.StepName + " " + e.Result
		}
	case activitylog.KindHelpAsked:
		return wf + " asked for help"
	case activitylog.KindHelpAnswered:
		return wf + " help answered"
	case activitylog.KindHelpParked:
		return wf + " parked (no answer)"
	case activitylog.KindHelpResumed:
		return wf + " resumed"
	default:
		return string(e.Kind)
	}
}
