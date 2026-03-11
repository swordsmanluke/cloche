package web

import (
	"bufio"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/ports"
)

// HandlerOption configures optional Handler dependencies.
type HandlerOption func(*Handler)

// WithContainerLogger sets the container runtime for fetching live logs.
func WithContainerLogger(c ContainerLogger) HandlerOption {
	return func(h *Handler) { h.container = c }
}

// WithContainerManager sets a full container manager (logs, inspect, remove).
func WithContainerManager(c ContainerManager) HandlerOption {
	return func(h *Handler) { h.container = c }
}

// WithLogBroadcaster sets the log broadcaster for SSE streaming.
func WithLogBroadcaster(b *logstream.Broadcaster) HandlerOption {
	return func(h *Handler) { h.logBroadcast = b }
}

// WithLogStore sets the log store for indexed log file lookups.
func WithLogStore(ls ports.LogStore) HandlerOption {
	return func(h *Handler) { h.logStore = ls }
}

//go:embed templates/*.html static/*
var content embed.FS

// Handler serves the web dashboard.
// ContainerLogger can retrieve logs from a container by ID.
type ContainerLogger interface {
	Logs(ctx context.Context, containerID string) (string, error)
}

// ContainerManager extends ContainerLogger with container lifecycle operations.
type ContainerManager interface {
	ContainerLogger
	Stop(ctx context.Context, containerID string) error
	Remove(ctx context.Context, containerID string) error
	Inspect(ctx context.Context, containerID string) (*ports.ContainerStatus, error)
}

type Handler struct {
	store        ports.RunStore
	captures     ports.CaptureStore
	logStore     ports.LogStore
	container    ContainerLogger
	logBroadcast *logstream.Broadcaster
	pages        map[string]*template.Template
	mux          *http.ServeMux
}

// NewHandler creates a web dashboard handler.
func NewHandler(store ports.RunStore, captures ports.CaptureStore, opts ...HandlerOption) (*Handler, error) {
	funcMap := template.FuncMap{
		"stateColor":       stateColor,
		"healthColor":      healthColor,
		"formatTime":       formatTime,
		"formatDuration":   formatDuration,
		"formatRunTiming":  formatRunTiming,
		"truncate":         truncate,
		"shortContainerID": shortContainerID,
	}

	base, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}

	pages := map[string]*template.Template{}
	for _, page := range []string{"projects", "runs", "run_detail", "project_detail"} {
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

	h.mux.HandleFunc("GET /{$}", h.handleProjectOverview)
	h.mux.HandleFunc("GET /projects/{name}/runs", h.handleProjectRuns)
	h.mux.HandleFunc("GET /runs", h.handleRunsList)
	h.mux.HandleFunc("GET /runs/{id}", h.handleRunDetail)
	h.mux.HandleFunc("GET /projects/{name}", h.handleProjectDetail)
	h.mux.HandleFunc("GET /api/projects", h.handleAPIProjects)
	h.mux.HandleFunc("GET /api/runs", h.handleAPIRuns)
	h.mux.HandleFunc("GET /api/runs/{id}", h.handleAPIRunDetail)
	h.mux.HandleFunc("GET /api/runs/{id}/steps/{step}/output", h.handleAPIStepOutput)
	h.mux.HandleFunc("POST /api/runs/{id}/stop", h.handleAPIStopRun)
	h.mux.HandleFunc("DELETE /api/runs/{id}/container", h.handleAPIDeleteContainer)
	h.mux.HandleFunc("DELETE /api/projects/{name}/containers", h.handleAPIDeleteProjectContainers)
	h.mux.HandleFunc("GET /api/projects/{name}/info", h.handleAPIProjectInfo)
	h.mux.HandleFunc("GET /api/projects/{name}/info/prompt-diff", h.handleAPIPromptDiff)
	h.mux.HandleFunc("GET /api/projects/{name}/workflows", h.handleAPIWorkflows)
	h.mux.HandleFunc("GET /api/projects/{name}/workflows/{workflow}/steps/{step}/content", h.handleAPIStepContent)
	h.mux.HandleFunc("GET /api/runs/{id}/stream", h.handleAPIStream)
	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// --- HTML handlers ---

// projectOverviewEntry holds data for a single project card on the landing page.
type projectOverviewEntry struct {
	Dir         string
	Label       string
	Health      domain.HealthResult
	RecentRuns  []recentRunDot
	ActiveCount int
}

// recentRunDot is a minimal representation of a run for the mini history display.
type recentRunDot struct {
	ID    string
	State string
}

const healthWindowSize = 10

func (h *Handler) handleProjectOverview(w http.ResponseWriter, r *http.Request) {
	projects, _ := h.store.ListProjects(r.Context())
	labels := projectLabels(projects)

	var entries []projectOverviewEntry
	for _, dir := range projects {
		runs, err := h.store.ListRunsByProject(r.Context(), dir, time.Time{})
		if err != nil {
			continue
		}

		// Convert []*domain.Run to []domain.Run for CalculateHealth.
		runValues := make([]domain.Run, len(runs))
		for i, rr := range runs {
			runValues[i] = *rr
		}

		health := domain.CalculateHealth(runValues, healthWindowSize)

		// Take the last N runs for the mini history (most recent first).
		n := healthWindowSize
		if n > len(runs) {
			n = len(runs)
		}
		dots := make([]recentRunDot, n)
		for i := 0; i < n; i++ {
			dots[i] = recentRunDot{
				ID:    runs[i].ID,
				State: string(runs[i].State),
			}
		}

		var activeCount int
		for _, rr := range runs {
			if rr.State == domain.RunStatePending || rr.State == domain.RunStateRunning {
				activeCount++
			}
		}

		entries = append(entries, projectOverviewEntry{
			Dir:         dir,
			Label:       labels[dir],
			Health:      health,
			RecentRuns:  dots,
			ActiveCount: activeCount,
		})
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Label < entries[j].Label
	})

	data := map[string]any{
		"Title":    "Projects",
		"Projects": entries,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["projects"].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

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

	// Count retained containers per project
	containerCounts := map[string]int{}
	allRuns, _ := h.store.ListRuns(r.Context(), time.Time{})
	for _, run := range allRuns {
		if run.ContainerKept {
			containerCounts[run.ProjectDir]++
		}
	}

	var projectList []projectEntry
	for _, dir := range projects {
		projectList = append(projectList, projectEntry{Dir: dir, Label: labels[dir], ContainerCount: containerCounts[dir]})
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

// handleProjectRuns redirects /projects/{name}/runs to /runs?project=<dir>.
func (h *Handler) handleProjectRuns(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	http.Redirect(w, r, "/runs?project="+url.QueryEscape(dir), http.StatusFound)
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

// containerState inspects the container for a run and returns one of:
//   - "running"   – container exists and is running
//   - "stopped"   – container exists, not running, not kept
//   - "available" – container exists, not running, kept (can be deleted)
//   - "removed"   – no container ID, no manager, or inspect failed
func (h *Handler) containerState(ctx context.Context, run *domain.Run) string {
	if run.ContainerID == "" {
		return "removed"
	}
	mgr, ok := h.container.(ContainerManager)
	if !ok {
		return "removed"
	}
	status, err := mgr.Inspect(ctx, run.ContainerID)
	if err != nil {
		return "removed"
	}
	if status.Running {
		return "running"
	}
	if run.ContainerKept {
		return "available"
	}
	return "stopped"
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

	// Fetch parent run if this is a child
	var parentRun *domain.Run
	if run.ParentRunID != "" {
		parentRun, _ = h.store.GetRun(r.Context(), run.ParentRunID)
	}

	// Fetch child runs
	childRuns, _ := h.store.ListChildRuns(r.Context(), id)

	data := map[string]any{
		"Title":          "Run " + run.ID,
		"Run":            run,
		"Steps":          steps,
		"Page":           "detail",
		"ContainerState": h.containerState(r.Context(), run),
		"ParentRun":      parentRun,
		"ChildRuns":      childRuns,
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
	Timing       string `json:"timing"`
	ContainerID  string `json:"container_id"`
	ErrorMessage string `json:"error_message"`
	Title        string `json:"title"`
	IsHost       bool   `json:"is_host"`
	ParentRunID  string `json:"parent_run_id,omitempty"`
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
	ContainerState string    `json:"container_state"`
	Steps          []apiStep `json:"steps"`
	ChildRuns      []apiRun  `json:"child_runs,omitempty"`
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
		Timing:       formatRunTiming(r.State, r.StartedAt, r.CompletedAt),
		ContainerID:  r.ContainerID,
		ErrorMessage: r.ErrorMessage,
		Title:        r.Title,
		IsHost:       r.IsHost,
		ParentRunID:  r.ParentRunID,
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

	type apiHealth struct {
		Status string `json:"status"`
		Passed int    `json:"passed"`
		Failed int    `json:"failed"`
		Total  int    `json:"total"`
	}
	type apiProject struct {
		Dir         string    `json:"dir"`
		Label       string    `json:"label"`
		Health      apiHealth `json:"health"`
		ActiveCount int       `json:"active_count"`
	}
	result := make([]apiProject, len(projects))
	for i, dir := range projects {
		runs, _ := h.store.ListRunsByProject(r.Context(), dir, time.Time{})
		runValues := make([]domain.Run, len(runs))
		for j, rr := range runs {
			runValues[j] = *rr
		}
		health := domain.CalculateHealth(runValues, healthWindowSize)
		var activeCount int
		for _, rr := range runs {
			if rr.State == domain.RunStatePending || rr.State == domain.RunStateRunning {
				activeCount++
			}
		}
		result[i] = apiProject{
			Dir:   dir,
			Label: labels[dir],
			Health: apiHealth{
				Status: string(health.Status),
				Passed: health.Passed,
				Failed: health.Failed,
				Total:  health.Total,
			},
			ActiveCount: activeCount,
		}
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

	// Fetch child runs for the API response
	var apiChildren []apiRun
	if children, err := h.store.ListChildRuns(r.Context(), id); err == nil {
		for _, c := range children {
			apiChildren = append(apiChildren, toAPIRun(c, labels))
		}
	}

	detail := apiRunDetail{
		apiRun:         toAPIRun(run, labels),
		ContainerState: h.containerState(r.Context(), run),
		Steps:          steps,
		ChildRuns:      apiChildren,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(detail)
}

func (h *Handler) handleAPIStepOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	step := r.PathValue("step")
	logType := r.URL.Query().Get("type")   // optional: "script", "llm"
	format := r.URL.Query().Get("format")  // optional: "raw" to skip parsing

	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	writeOutput := func(data []byte) {
		if format != "raw" {
			data = logstream.ParseClaudeStream(data)
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write(data)
	}

	// Try log index first for fast lookup
	if h.logStore != nil {
		logFiles, err := h.logStore.GetLogFilesByStep(r.Context(), id, step)
		if err == nil && len(logFiles) > 0 {
			// If type filter specified, narrow results
			for _, lf := range logFiles {
				if logType != "" && lf.FileType != logType {
					continue
				}
				data, readErr := os.ReadFile(lf.FilePath)
				if readErr == nil && len(data) > 0 {
					writeOutput(data)
					return
				}
			}
		}
	}

	outputDir := filepath.Join(run.ProjectDir, ".cloche", id, "output")

	// Fall back to file path conventions
	// If type is "llm", try llm-<step>.log
	if logType == "llm" {
		llmPath := filepath.Join(outputDir, "llm-"+step+".log")
		data, err := os.ReadFile(llmPath)
		if err == nil && len(data) > 0 {
			writeOutput(data)
			return
		}
	}

	// Try per-step output file
	outputPath := filepath.Join(outputDir, step+".log")
	data, err := os.ReadFile(outputPath)
	if err == nil && len(data) > 0 {
		writeOutput(data)
		return
	}

	// Do NOT fall back to container.log or live docker logs here — those
	// contain unfiltered output from ALL steps and would show the wrong
	// content for this specific step (the root cause of web-UI log mismatches).

	http.Error(w, "step output not found", http.StatusNotFound)
}

func (h *Handler) handleAPIStopRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "run not found"})
		return
	}

	// Only allow stopping active runs
	if run.State != domain.RunStatePending && run.State != domain.RunStateRunning {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "run is not active"})
		return
	}

	mgr, ok := h.container.(ContainerManager)
	if !ok || run.ContainerID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "container management not available"})
		return
	}

	if err := mgr.Stop(r.Context(), run.ContainerID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to stop run: %v", err)})
		return
	}

	run.Complete(domain.RunStateCancelled)
	if err := h.store.UpdateRun(r.Context(), run); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to update run: %v", err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (h *Handler) handleAPIDeleteContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "run not found"})
		return
	}

	mgr, ok := h.container.(ContainerManager)
	if !ok || run.ContainerID == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "container management not available"})
		return
	}

	if err := mgr.Remove(r.Context(), run.ContainerID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to remove container: %v", err)})
		return
	}

	run.ContainerKept = false
	if err := h.store.UpdateRun(r.Context(), run); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to update run: %v", err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleAPIDeleteProjectContainers mass-deletes all retained/exited containers for a project.
func (h *Handler) handleAPIDeleteProjectContainers(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	mgr, ok := h.container.(ContainerManager)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "container management not available"})
		return
	}

	runs, err := h.store.ListRunsByProject(r.Context(), dir, time.Time{})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to list runs"})
		return
	}

	var deleted int
	var errors []string
	for _, run := range runs {
		if !run.ContainerKept || run.ContainerID == "" {
			continue
		}
		// Check container is not running
		status, err := mgr.Inspect(r.Context(), run.ContainerID)
		if err != nil {
			// Container already gone — just clear the flag
			run.ContainerKept = false
			h.store.UpdateRun(r.Context(), run)
			deleted++
			continue
		}
		if status.Running {
			continue // skip running containers
		}
		if err := mgr.Remove(r.Context(), run.ContainerID); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", run.ID, err))
			continue
		}
		run.ContainerKept = false
		if err := h.store.UpdateRun(r.Context(), run); err != nil {
			errors = append(errors, fmt.Sprintf("%s: update failed: %v", run.ID, err))
			continue
		}
		deleted++
	}

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"deleted": deleted}
	if len(errors) > 0 {
		resp["errors"] = errors
	}
	json.NewEncoder(w).Encode(resp)
}

// handleAPIStream serves an SSE stream of log lines for a run.
// For active runs, it subscribes to the live broadcaster.
// For completed runs, it serves the archived full.log then closes.
func (h *Handler) handleAPIStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	isComplete := run.State == domain.RunStateSucceeded ||
		run.State == domain.RunStateFailed ||
		run.State == domain.RunStateCancelled

	if isComplete {
		// Serve archived full.log
		h.streamFullLog(w, flusher, run)
		// Send done event
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", string(run.State))
		flusher.Flush()
		return
	}

	// Active run: subscribe to live broadcaster
	if h.logBroadcast == nil {
		http.Error(w, "log streaming not available", http.StatusServiceUnavailable)
		return
	}

	sub := h.logBroadcast.Subscribe(id)
	defer h.logBroadcast.Unsubscribe(id, sub)

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case line, ok := <-sub.C:
			if !ok {
				// Stream finished (run completed)
				fmt.Fprintf(w, "event: done\ndata: completed\n\n")
				flusher.Flush()
				return
			}
			line = parseLLMLogLine(line)
			if line.Type == "" {
				continue // skip protocol-only llm lines
			}
			data, _ := json.Marshal(line)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// logLineRegex parses "[timestamp] [type] content" format from full.log.
var logLineRegex = regexp.MustCompile(`^\[([^\]]+)\] \[([^\]]+)\] (.*)$`)

// streamFullLog reads the archived full.log file and sends its entries as SSE events.
// LLM-type lines are parsed from raw Claude JSON into human-readable text.
func (h *Handler) streamFullLog(w http.ResponseWriter, flusher http.Flusher, run *domain.Run) {
	logPath := filepath.Join(run.ProjectDir, ".cloche", run.ID, "output", "full.log")
	f, err := os.Open(logPath)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		text := scanner.Text()
		line := parseFullLogLine(text)
		line = parseLLMLogLine(line)
		if line.Type == "" {
			continue // skip protocol-only llm lines
		}
		data, _ := json.Marshal(line)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
}

// parseLLMLogLine parses a LogLine whose type is "llm". If the content is
// Claude stream JSON, it extracts the human-readable text. Protocol-only
// events return a LogLine with an empty Type (caller should skip it).
// Non-llm lines are returned unchanged.
func parseLLMLogLine(line logstream.LogLine) logstream.LogLine {
	if line.Type != "llm" {
		return line
	}
	text, ok := logstream.ParseClaudeLine([]byte(line.Content))
	if !ok {
		return logstream.LogLine{} // signal to skip
	}
	line.Content = text
	return line
}

// parseFullLogLine parses a "[timestamp] [type] content" line into a LogLine.
func parseFullLogLine(text string) logstream.LogLine {
	m := logLineRegex.FindStringSubmatch(text)
	if m == nil {
		return logstream.LogLine{
			Timestamp: time.Now().UTC().Format(time.RFC3339),
			Type:      "script",
			Content:   text,
		}
	}
	return logstream.LogLine{
		Timestamp: m[1],
		Type:      m[2],
		Content:   m[3],
	}
}

// --- Project detail handlers ---

// resolveProjectDir resolves a project label from the URL to its directory path.
// Returns (dir, label, ok). Writes a JSON error response if not found.
func (h *Handler) resolveProjectDir(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	label := r.PathValue("name")
	projects, _ := h.store.ListProjects(r.Context())
	labels := projectLabels(projects)
	for dir, l := range labels {
		if l == label {
			return dir, label, true
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]string{"error": "project not found"})
	return "", "", false
}

func (h *Handler) handleProjectDetail(w http.ResponseWriter, r *http.Request) {
	dir, label, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	runs, _ := h.store.ListRunsByProject(r.Context(), dir, time.Time{})
	n := 10
	if n > len(runs) {
		n = len(runs)
	}
	dots := make([]recentRunDot, n)
	for i := 0; i < n; i++ {
		dots[i] = recentRunDot{ID: runs[i].ID, State: string(runs[i].State)}
	}

	var containerCount int
	for _, run := range runs {
		if run.ContainerKept {
			containerCount++
		}
	}

	data := map[string]any{
		"Title":          label,
		"Label":          label,
		"Dir":            dir,
		"RecentRuns":     dots,
		"ContainerCount": containerCount,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["project_detail"].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleAPIProjectInfo(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	// Docker image: read FROM line from Dockerfile
	dockerImage := ""
	dockerfilePath := filepath.Join(dir, ".cloche", "Dockerfile")
	if data, err := os.ReadFile(dockerfilePath); err == nil {
		// Find the last FROM line (final stage)
		for _, line := range strings.Split(string(data), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToUpper(trimmed), "FROM ") {
				parts := strings.Fields(trimmed)
				if len(parts) >= 2 {
					dockerImage = parts[1]
				}
			}
		}
	}

	// Version
	version := 0
	versionPath := filepath.Join(dir, ".cloche", "version")
	if data, err := os.ReadFile(versionPath); err == nil {
		if v, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
			version = v
		}
	}

	// Prompt files history
	type commitEntry struct {
		SHA     string `json:"sha"`
		Date    string `json:"date"`
		Message string `json:"message"`
	}
	type promptFile struct {
		Path    string        `json:"path"`
		History []commitEntry `json:"history"`
	}

	var promptFiles []promptFile
	promptsDir := filepath.Join(dir, ".cloche", "prompts")
	if entries, err := os.ReadDir(promptsDir); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			relPath := filepath.Join(".cloche", "prompts", e.Name())
			cmd := exec.Command("git", "log", "--follow", "--format=%H %aI %s", "--", relPath)
			cmd.Dir = dir
			out, err := cmd.Output()
			if err != nil {
				continue
			}
			var history []commitEntry
			for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, " ", 3)
				if len(parts) < 3 {
					continue
				}
				// Parse date to short form
				dateStr := parts[1]
				if t, err := time.Parse(time.RFC3339, dateStr); err == nil {
					dateStr = t.Format("2006-01-02")
				}
				history = append(history, commitEntry{
					SHA:     parts[0][:7],
					Date:    dateStr,
					Message: parts[2],
				})
			}
			promptFiles = append(promptFiles, promptFile{Path: relPath, History: history})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"docker_image": dockerImage,
		"version":      version,
		"prompt_files": promptFiles,
	})
}

func (h *Handler) handleAPIPromptDiff(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	file := r.URL.Query().Get("file")
	sha := r.URL.Query().Get("sha")
	if file == "" || sha == "" {
		http.Error(w, "file and sha required", http.StatusBadRequest)
		return
	}

	// Validate sha is alphanumeric to prevent injection
	for _, c := range sha {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			http.Error(w, "invalid sha", http.StatusBadRequest)
			return
		}
	}

	cmd := exec.Command("git", "diff", sha+"^.."+sha, "--", file)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		http.Error(w, "diff not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
}

func (h *Handler) handleAPIWorkflows(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	clocheDir := filepath.Join(dir, ".cloche")
	entries, err := os.ReadDir(clocheDir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	type apiWire struct {
		From   string `json:"from"`
		Result string `json:"result"`
		To     string `json:"to"`
	}
	type apiStepDef struct {
		Name    string            `json:"name"`
		Type    string            `json:"type"`
		Results []string          `json:"results"`
		Config  map[string]string `json:"config"`
	}
	type apiWorkflow struct {
		Name      string       `json:"name"`
		File      string       `json:"file"`
		Steps     []apiStepDef `json:"steps"`
		Wires     []apiWire    `json:"wires"`
		EntryStep string       `json:"entry_step"`
	}

	var workflows []apiWorkflow
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cloche") || e.Name() == "host.cloche" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(clocheDir, e.Name()))
		if err != nil {
			continue
		}
		wf, err := dsl.ParseForContainer(string(data))
		if err != nil {
			continue
		}
		var steps []apiStepDef
		for _, s := range wf.Steps {
			steps = append(steps, apiStepDef{
				Name:    s.Name,
				Type:    string(s.Type),
				Results: s.Results,
				Config:  s.Config,
			})
		}
		// Sort steps by name for consistent ordering
		sort.Slice(steps, func(i, j int) bool { return steps[i].Name < steps[j].Name })

		var wires []apiWire
		for _, wire := range wf.Wiring {
			wires = append(wires, apiWire{From: wire.From, Result: wire.Result, To: wire.To})
		}

		workflows = append(workflows, apiWorkflow{
			Name:      wf.Name,
			File:      filepath.Join(".cloche", e.Name()),
			Steps:     steps,
			Wires:     wires,
			EntryStep: wf.EntryStep,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workflows)
}

func (h *Handler) handleAPIStepContent(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	workflowName := r.PathValue("workflow")
	stepName := r.PathValue("step")

	// Find the workflow file
	clocheDir := filepath.Join(dir, ".cloche")
	var wf *domain.Workflow
	entries, _ := os.ReadDir(clocheDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cloche") || e.Name() == "host.cloche" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(clocheDir, e.Name()))
		if err != nil {
			continue
		}
		parsed, err := dsl.ParseForContainer(string(data))
		if err != nil {
			continue
		}
		if parsed.Name == workflowName {
			wf = parsed
			break
		}
	}
	if wf == nil {
		http.Error(w, "workflow not found", http.StatusNotFound)
		return
	}

	step, ok := wf.Steps[stepName]
	if !ok {
		http.Error(w, "step not found", http.StatusNotFound)
		return
	}

	// Try to read the referenced file from step config
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")

	if prompt := step.Config["prompt"]; prompt != "" {
		content, err := resolveFileRef(prompt, dir)
		if err == nil {
			w.Write([]byte(content))
			return
		}
	}
	if cmd := step.Config["command"]; cmd != "" {
		content, _ := resolveFileRef(cmd, dir)
		w.Write([]byte(content))
		return
	}
	if script := step.Config["script"]; script != "" {
		content, _ := resolveFileRef(script, dir)
		w.Write([]byte(content))
		return
	}

	http.Error(w, "no content available", http.StatusNotFound)
}

// --- Template helpers ---

// resolveFileRef resolves file("path") DSL syntax to actual file contents.
// If the value uses file() syntax, the referenced file is read from disk.
// Otherwise the value is returned as-is.
func resolveFileRef(value, baseDir string) (string, error) {
	if strings.HasPrefix(value, `file("`) && strings.HasSuffix(value, `")`) {
		path := value[6 : len(value)-2]
		data, err := os.ReadFile(filepath.Join(baseDir, path))
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	return value, nil
}

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

func healthColor(status domain.HealthStatus) string {
	switch status {
	case domain.HealthGreen:
		return "green"
	case domain.HealthYellow:
		return "yellow"
	case domain.HealthRed:
		return "red"
	case domain.HealthBlue:
		return "blue"
	default:
		return "grey"
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

// formatSmartDuration formats a duration into a human-friendly short string.
// Examples: "3s", "2m", "1h20m", "3h".
func formatSmartDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h == 0 {
		return fmt.Sprintf("%dm", m)
	}
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh%dm", h, m)
}

// roundRelativeTime rounds a duration to neat display units:
// <1m -> "<1m ago", then 1m,5m,10m,15m,30m,45m,1h,2h,3h,...24h,
// then days.
func roundRelativeTime(d time.Duration) string {
	if d < time.Minute {
		return "<1m ago"
	}
	mins := int(d.Minutes())
	hours := int(d.Hours())
	days := hours / 24

	if days > 0 {
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	}
	if hours >= 1 {
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	}
	// Round minutes to neat breakpoints
	switch {
	case mins < 3:
		return "1m ago"
	case mins < 8:
		return "5m ago"
	case mins < 13:
		return "10m ago"
	case mins < 20:
		return "15m ago"
	case mins < 38:
		return "30m ago"
	case mins < 53:
		return "45m ago"
	default:
		return "1 hour ago"
	}
}

// formatRunTiming returns a smart timing string for display on the Runs page.
// Running: "5m" (just duration so far)
// Completed: "20m, 1 hour ago"
// Pending: ""
func formatRunTiming(state domain.RunState, startedAt, completedAt time.Time) string {
	return formatRunTimingAt(state, startedAt, completedAt, time.Now())
}

// formatRunTimingAt is the testable version of formatRunTiming with an explicit "now".
func formatRunTimingAt(state domain.RunState, startedAt, completedAt, now time.Time) string {
	if startedAt.IsZero() {
		return ""
	}
	switch state {
	case domain.RunStateRunning:
		d := now.Sub(startedAt)
		return formatSmartDuration(d)
	default:
		if completedAt.IsZero() {
			return ""
		}
		d := completedAt.Sub(startedAt)
		ago := now.Sub(completedAt)
		return formatSmartDuration(d) + ", " + roundRelativeTime(ago)
	}
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
	Dir            string
	Label          string
	ContainerCount int
}
