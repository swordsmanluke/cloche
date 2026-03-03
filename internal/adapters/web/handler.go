package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
)

// HandlerOption configures optional Handler dependencies.
type HandlerOption func(*Handler)

// WithContainerLogger sets the container runtime for fetching live logs.
func WithContainerLogger(c ContainerLogger) HandlerOption {
	return func(h *Handler) { h.container = c }
}

//go:embed templates/*.html static/*
var content embed.FS

// Handler serves the web dashboard.
// ContainerLogger can retrieve logs from a container by ID.
type ContainerLogger interface {
	Logs(ctx context.Context, containerID string) (string, error)
}

type Handler struct {
	store     ports.RunStore
	captures  ports.CaptureStore
	container ContainerLogger
	pages     map[string]*template.Template
	mux       *http.ServeMux
}

// NewHandler creates a web dashboard handler.
func NewHandler(store ports.RunStore, captures ports.CaptureStore, opts ...HandlerOption) (*Handler, error) {
	funcMap := template.FuncMap{
		"stateColor":       stateColor,
		"formatTime":       formatTime,
		"formatDuration":   formatDuration,
		"truncate":         truncate,
		"shortContainerID": shortContainerID,
	}

	base, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}

	pages := map[string]*template.Template{}
	for _, page := range []string{"runs", "run_detail"} {
		clone, err := base.Clone()
		if err != nil {
			return nil, fmt.Errorf("clone layout for %s: %w", page, err)
		}
		_, err = clone.ParseFS(content, "templates/"+page+".html")
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", page, err)
		}
		pages[page] = clone
	}

	h := &Handler{
		store:    store,
		captures: captures,
		pages:    pages,
		mux:      http.NewServeMux(),
	}
	for _, opt := range opts {
		opt(h)
	}

	staticFS, err := fs.Sub(content, "static")
	if err != nil {
		return nil, fmt.Errorf("static sub-fs: %w", err)
	}

	h.mux.HandleFunc("GET /{$}", h.handleRunsList)
	h.mux.HandleFunc("GET /runs/{id}", h.handleRunDetail)
	h.mux.HandleFunc("GET /api/projects", h.handleAPIProjects)
	h.mux.HandleFunc("GET /api/runs", h.handleAPIRuns)
	h.mux.HandleFunc("GET /api/runs/{id}", h.handleAPIRunDetail)
	h.mux.HandleFunc("GET /api/runs/{id}/steps/{step}/output", h.handleAPIStepOutput)
	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// --- HTML handlers ---

func (h *Handler) handleRunsList(w http.ResponseWriter, r *http.Request) {
	projectFilter := r.URL.Query().Get("project")

	var runs []*domain.Run
	var err error
	if projectFilter != "" {
		runs, err = h.store.ListRunsByProject(r.Context(), projectFilter, time.Time{})
	} else {
		runs, err = h.store.ListRuns(r.Context(), time.Time{})
	}
	if err != nil {
		http.Error(w, "failed to list runs", http.StatusInternalServerError)
		return
	}

	projects, _ := h.store.ListProjects(r.Context())
	labels := projectLabels(projects)

	var projectList []projectEntry
	for _, dir := range projects {
		projectList = append(projectList, projectEntry{Dir: dir, Label: labels[dir]})
	}
	sort.Slice(projectList, func(i, j int) bool {
		return projectList[i].Label < projectList[j].Label
	})

	data := map[string]any{
		"Title":         "Runs",
		"Runs":          runs,
		"Projects":      projectList,
		"ProjectFilter": projectFilter,
		"ProjectLabels": labels,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["runs"].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// stepEntry is a merged view of a step execution for the template.
type stepEntry struct {
	Index     int
	StepName  string
	Result    string
	StartedAt time.Time
	Duration  string
}

// mergeCaptures collapses started/completed capture pairs into single entries.
func mergeCaptures(caps []*domain.StepExecution) []stepEntry {
	var entries []stepEntry
	// Track pending started rows by step name (LIFO for retries)
	pending := map[string]*domain.StepExecution{}

	for _, c := range caps {
		if c.Result == "" {
			// step_started row
			pending[c.StepName] = c
			continue
		}
		// step_completed row — merge with pending started if available
		startedAt := c.StartedAt
		if started := pending[c.StepName]; started != nil {
			startedAt = started.StartedAt
			delete(pending, c.StepName)
		}
		entries = append(entries, stepEntry{
			Index:     len(entries),
			StepName:  c.StepName,
			Result:    c.Result,
			StartedAt: startedAt,
			Duration:  formatDuration(startedAt, c.CompletedAt),
		})
	}

	// Append any started-but-not-completed steps (still running)
	for _, c := range caps {
		if c.Result == "" {
			if _, used := pending[c.StepName]; !used {
				continue
			}
			entries = append(entries, stepEntry{
				Index:     len(entries),
				StepName:  c.StepName,
				StartedAt: c.StartedAt,
			})
			delete(pending, c.StepName)
		}
	}

	return entries
}

func (h *Handler) handleRunDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	caps, err := h.captures.GetCaptures(r.Context(), id)
	if err != nil {
		caps = nil // non-fatal
	}

	steps := mergeCaptures(caps)

	data := map[string]any{
		"Title": "Run " + run.ID,
		"Run":   run,
		"Steps": steps,
		"Page":  "detail",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["run_detail"].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- JSON API handlers ---

type apiRun struct {
	ID           string `json:"id"`
	WorkflowName string `json:"workflow_name"`
	ProjectDir   string `json:"project_dir"`
	ProjectLabel string `json:"project_label"`
	State        string `json:"state"`
	StartedAt    string `json:"started_at"`
	CompletedAt  string `json:"completed_at"`
	ContainerID  string `json:"container_id"`
	ErrorMessage string `json:"error_message"`
}

type apiStep struct {
	StepName    string `json:"step_name"`
	Result      string `json:"result"`
	StartedAt   string `json:"started_at"`
	CompletedAt string `json:"completed_at"`
	Duration    string `json:"duration"`
}

type apiRunDetail struct {
	apiRun
	Steps []apiStep `json:"steps"`
}

func toAPIRun(r *domain.Run, labels map[string]string) apiRun {
	return apiRun{
		ID:           r.ID,
		WorkflowName: r.WorkflowName,
		ProjectDir:   r.ProjectDir,
		ProjectLabel: labels[r.ProjectDir],
		State:        string(r.State),
		StartedAt:    formatTime(r.StartedAt),
		CompletedAt:  formatTime(r.CompletedAt),
		ContainerID:  r.ContainerID,
		ErrorMessage: r.ErrorMessage,
	}
}

func (h *Handler) handleAPIRuns(w http.ResponseWriter, r *http.Request) {
	projectFilter := r.URL.Query().Get("project")

	var runs []*domain.Run
	var err error
	if projectFilter != "" {
		runs, err = h.store.ListRunsByProject(r.Context(), projectFilter, time.Time{})
	} else {
		runs, err = h.store.ListRuns(r.Context(), time.Time{})
	}
	if err != nil {
		http.Error(w, "failed to list runs", http.StatusInternalServerError)
		return
	}

	projects, _ := h.store.ListProjects(r.Context())
	labels := projectLabels(projects)

	result := make([]apiRun, len(runs))
	for i, run := range runs {
		result[i] = toAPIRun(run, labels)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *Handler) handleAPIProjects(w http.ResponseWriter, r *http.Request) {
	projects, err := h.store.ListProjects(r.Context())
	if err != nil {
		http.Error(w, "failed to list projects", http.StatusInternalServerError)
		return
	}
	labels := projectLabels(projects)

	type apiProject struct {
		Dir   string `json:"dir"`
		Label string `json:"label"`
	}
	result := make([]apiProject, len(projects))
	for i, dir := range projects {
		result[i] = apiProject{Dir: dir, Label: labels[dir]}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *Handler) handleAPIRunDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	caps, err := h.captures.GetCaptures(r.Context(), id)
	if err != nil {
		caps = nil
	}

	projects, _ := h.store.ListProjects(r.Context())
	labels := projectLabels(projects)

	merged := mergeCaptures(caps)
	steps := make([]apiStep, len(merged))
	for i, e := range merged {
		steps[i] = apiStep{
			StepName:  e.StepName,
			Result:    e.Result,
			StartedAt: formatTime(e.StartedAt),
			Duration:  e.Duration,
		}
	}

	detail := apiRunDetail{
		apiRun: toAPIRun(run, labels),
		Steps:  steps,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(detail)
}

func (h *Handler) handleAPIStepOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	step := r.PathValue("step")

	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	outputDir := filepath.Join(run.ProjectDir, ".cloche", id, "output")

	// Try per-step output first, fall back to container.log, then live docker logs
	outputPath := filepath.Join(outputDir, step+".log")
	data, err := os.ReadFile(outputPath)
	if err == nil && len(data) > 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
		return
	}

	containerLog := filepath.Join(outputDir, "container.log")
	data, err = os.ReadFile(containerLog)
	if err == nil && len(data) > 0 {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
		return
	}

	// Last resort: try live docker logs from still-existing container
	if h.container != nil && run.ContainerID != "" {
		if logs, logErr := h.container.Logs(r.Context(), run.ContainerID); logErr == nil && logs != "" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write([]byte(logs))
			return
		}
	}

	http.Error(w, "step output not found", http.StatusNotFound)
}

// --- Template helpers ---

func stateColor(state domain.RunState) string {
	switch state {
	case domain.RunStatePending:
		return "pending"
	case domain.RunStateRunning:
		return "running"
	case domain.RunStateSucceeded:
		return "succeeded"
	case domain.RunStateFailed:
		return "failed"
	case domain.RunStateCancelled:
		return "cancelled"
	default:
		return "pending"
	}
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02 15:04:05")
}

func formatDuration(start, end time.Time) string {
	if start.IsZero() || end.IsZero() {
		return ""
	}
	d := end.Sub(start)
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func shortContainerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// projectLabels builds a mapping from full project directory paths to short
// display labels. Each label is the final directory component (e.g.
// "myproject" from "/home/user/workspace/myproject"). When two projects share
// the same final name, the parent directory is prepended to disambiguate
// (e.g. "foo/bar" vs "baz/bar").
func projectLabels(dirs []string) map[string]string {
	labels := make(map[string]string, len(dirs))
	// Group dirs by their base name to detect conflicts.
	byBase := map[string][]string{}
	for _, d := range dirs {
		base := filepath.Base(d)
		byBase[base] = append(byBase[base], d)
	}
	for base, paths := range byBase {
		if len(paths) == 1 {
			labels[paths[0]] = base
		} else {
			for _, p := range paths {
				parent := filepath.Base(filepath.Dir(p))
				labels[p] = parent + "/" + base
			}
		}
	}
	return labels
}

type projectEntry struct {
	Dir   string
	Label string
}
