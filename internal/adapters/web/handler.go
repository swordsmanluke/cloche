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
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
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
		"truncate":         truncate,
		"shortContainerID": shortContainerID,
	}

	base, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}

	pages := map[string]*template.Template{}
	for _, page := range []string{"projects", "runs", "run_detail"} {
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
	h.mux.HandleFunc("GET /runs", h.handleRunsList)
	h.mux.HandleFunc("GET /runs/{id}", h.handleRunDetail)
	h.mux.HandleFunc("GET /api/projects", h.handleAPIProjects)
	h.mux.HandleFunc("GET /api/runs", h.handleAPIRuns)
	h.mux.HandleFunc("GET /api/runs/{id}", h.handleAPIRunDetail)
	h.mux.HandleFunc("GET /api/runs/{id}/steps/{step}/output", h.handleAPIStepOutput)
	h.mux.HandleFunc("DELETE /api/runs/{id}/container", h.handleAPIDeleteContainer)
	h.mux.HandleFunc("POST /api/projects/{name}/trigger", h.handleAPITriggerOrchestrator)
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
	Dir        string
	Label      string
	Health     domain.HealthResult
	RecentRuns []recentRunDot
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

	containerAvailable := false
	if run.ContainerKept && run.ContainerID != "" {
		if mgr, ok := h.container.(ContainerManager); ok {
			if _, err := mgr.Inspect(r.Context(), run.ContainerID); err == nil {
				containerAvailable = true
			}
		}
	}

	data := map[string]any{
		"Title":              "Run " + run.ID,
		"Run":                run,
		"Steps":              steps,
		"Page":               "detail",
		"ContainerAvailable": containerAvailable,
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
	logType := r.URL.Query().Get("type") // optional: "script", "llm"

	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
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
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
					w.Write(data)
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
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Write(data)
			return
		}
	}

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

func (h *Handler) handleAPITriggerOrchestrator(w http.ResponseWriter, r *http.Request) {
	// Placeholder endpoint — orchestrator integration is a separate feature.
	// Returns 202 Accepted to indicate the request was received.
	name := r.PathValue("name")
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "project": name})
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
			data, _ := json.Marshal(line)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// logLineRegex parses "[timestamp] [type] content" format from full.log.
var logLineRegex = regexp.MustCompile(`^\[([^\]]+)\] \[([^\]]+)\] (.*)$`)

// streamFullLog reads the archived full.log file and sends its entries as SSE events.
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
		data, _ := json.Marshal(line)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
	}
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
