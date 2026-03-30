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
	"github.com/cloche-dev/cloche/internal/version"
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

// WithTaskProvider sets the task provider for querying orchestration loop task state.
func WithTaskProvider(tp TaskProvider) HandlerOption {
	return func(h *Handler) { h.taskProvider = tp }
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

// TaskEntry represents a task with its assignment state for external consumers.
type TaskEntry struct {
	ID          string            `json:"id"`
	Status      string            `json:"status"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	Assigned    bool              `json:"assigned"`
	AssignedAt  string            `json:"assigned_at,omitempty"`
	RunID       string            `json:"run_id,omitempty"`
	Stale       bool              `json:"stale,omitempty"`
}

// TaskProvider retrieves task pipeline state for a project's orchestration loop.
type TaskProvider interface {
	GetLoopTasks(projectDir string) []TaskEntry
	ReleaseTask(ctx context.Context, projectDir string, taskID string) error
}

type Handler struct {
	store        ports.RunStore
	captures     ports.CaptureStore
	logStore     ports.LogStore
	container    ContainerLogger
	logBroadcast *logstream.Broadcaster
	taskProvider TaskProvider
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
		"jsonMap":          jsonMap,
		"clocheVersion":    version.Version,
	}

	base, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}

	pages := map[string]*template.Template{}
	for _, page := range []string{"projects", "runs", "run_detail", "project_detail", "task_detail", "failed_tasks"} {
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
	h.mux.HandleFunc("GET /api/projects/{name}/runs", h.handleAPIProjectRuns)
	h.mux.HandleFunc("GET /api/runs", h.handleAPIRuns)
	h.mux.HandleFunc("GET /api/runs/{id}", h.handleAPIRunDetail)
	h.mux.HandleFunc("GET /api/runs/{id}/steps/{step}/output", h.handleAPIStepOutput)
	h.mux.HandleFunc("POST /api/runs/{id}/stop", h.handleAPIStopRun)
	h.mux.HandleFunc("DELETE /api/runs/{id}/container", h.handleAPIDeleteContainer)
	h.mux.HandleFunc("DELETE /api/projects/{name}/containers", h.handleAPIDeleteProjectContainers)
	h.mux.HandleFunc("DELETE /api/containers", h.handleAPIDeleteAllContainers)
	h.mux.HandleFunc("GET /api/projects/{name}/usage", h.handleAPIProjectUsage)
	h.mux.HandleFunc("GET /api/projects/{name}/info", h.handleAPIProjectInfo)
	h.mux.HandleFunc("GET /api/projects/{name}/info/prompt-diff", h.handleAPIPromptDiff)
	h.mux.HandleFunc("GET /api/projects/{name}/workflows", h.handleAPIWorkflows)
	h.mux.HandleFunc("GET /api/projects/{name}/workflows/{workflow}/steps/{step}/content", h.handleAPIStepContent)
	h.mux.HandleFunc("GET /api/projects/{name}/tasks", h.handleAPITasks)
	h.mux.HandleFunc("POST /api/projects/{name}/tasks/{taskId}/release", h.handleAPIReleaseTask)
	h.mux.HandleFunc("GET /api/tasks", h.handleAPIAllTasks)
	h.mux.HandleFunc("GET /api/runs/{id}/logs", h.handleAPILogs)
	h.mux.HandleFunc("GET /api/runs/{id}/stream", h.handleAPIStream)
	h.mux.HandleFunc("GET /api/attempts/{id}/stream", h.handleAPIAttemptStream)
	h.mux.HandleFunc("GET /api/attempts/{id}/logs", h.handleAPIAttemptLogs)
	h.mux.HandleFunc("GET /tasks/{taskID}", h.handleTaskDetail)
	h.mux.HandleFunc("GET /failed-tasks", h.handleFailedTasksDashboard)
	h.mux.HandleFunc("GET /api/failed-tasks", h.handleAPIFailedTasks)
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

// taskSummaryEntry holds a summary of a task for the landing page task list.
type taskSummaryEntry struct {
	TaskID       string `json:"task_id"`
	TaskTitle    string `json:"task_title,omitempty"`
	ProjectLabel string `json:"project_label,omitempty"`
	Status       string `json:"status"`
	AttemptCount int    `json:"attempt_count"`
	LatestResult string `json:"latest_result,omitempty"`
	LatestTime   string `json:"latest_time,omitempty"`
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

// buildTaskSummaries derives a task summary list from a set of runs.
// Each unique TaskID produces one summary entry with status, attempt count, and latest result.
func buildTaskSummaries(runs []*domain.Run, labels map[string]string, taskTitles map[string]string) []taskSummaryEntry {
	// Build children map for computing per-attempt status.
	parentMap := map[string][]*domain.Run{}
	for _, r := range runs {
		if r.ParentRunID != "" {
			parentMap[r.ParentRunID] = append(parentMap[r.ParentRunID], r)
		}
	}

	// Group top-level runs by task ID (excluding list-tasks runs).
	taskRuns := map[string][]*domain.Run{}
	taskOrder := []string{} // preserve first-seen order (sorted by most active)
	seenTask := map[string]bool{}

	var sorted []*domain.Run
	for _, r := range runs {
		if r.WorkflowName != "list-tasks" && r.TaskID != "" && r.ParentRunID == "" {
			sorted = append(sorted, r)
		}
	}
	// Sort: running first, then by started_at desc
	sort.SliceStable(sorted, func(i, j int) bool {
		iRunning := sorted[i].State == domain.RunStateRunning
		jRunning := sorted[j].State == domain.RunStateRunning
		if iRunning != jRunning {
			return iRunning
		}
		return sorted[i].StartedAt.After(sorted[j].StartedAt)
	})

	for _, r := range sorted {
		taskRuns[r.TaskID] = append(taskRuns[r.TaskID], r)
		if !seenTask[r.TaskID] {
			seenTask[r.TaskID] = true
			taskOrder = append(taskOrder, r.TaskID)
		}
	}

	var result []taskSummaryEntry
	for _, tid := range taskOrder {
		group := taskRuns[tid]
		if len(group) == 0 {
			continue
		}
		// Task status reflects the latest attempt (group[0]) and its children.
		latestRun := group[0]
		latestAttemptRuns := append([]*domain.Run{latestRun}, parentMap[latestRun.ID]...)
		status := taskAggregateStatus(latestAttemptRuns)
		latestResult := string(latestRun.State)
		latestTime := formatTime(latestRun.StartedAt)

		result = append(result, taskSummaryEntry{
			TaskID:       tid,
			TaskTitle:    taskTitles[tid],
			ProjectLabel: labels[latestRun.ProjectDir],
			Status:       status,
			AttemptCount: len(group),
			LatestResult: latestResult,
			LatestTime:   latestTime,
		})
	}
	return result
}

// taskAttemptEntry holds a single attempt's summary for the task drill-down page.
type taskAttemptEntry struct {
	AttemptNum int
	AttemptID  string
	Status     string
	StartedAt  string
	Runs       []apiRun
}

// handleTaskDetail renders the task drill-down page showing all attempts for a task.
func (h *Handler) handleTaskDetail(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")

	runs, err := h.store.ListRunsFiltered(r.Context(), domain.RunListFilter{TaskID: taskID})
	if err != nil || len(runs) == 0 {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}

	projects, _ := h.store.ListProjects(r.Context())
	labels := projectLabels(projects)
	taskTitles := h.taskTitlesFromRuns(runs)

	// Build lookup and parent→children map for top-level runs in this task.
	byID := map[string]*domain.Run{}
	for _, r := range runs {
		byID[r.ID] = r
	}
	parentMap := map[string][]*domain.Run{}
	var topLevel []*domain.Run
	for _, rr := range runs {
		if rr.ParentRunID != "" && byID[rr.ParentRunID] != nil {
			parentMap[rr.ParentRunID] = append(parentMap[rr.ParentRunID], rr)
		} else {
			topLevel = append(topLevel, rr)
		}
	}

	// Sort top-level: running first, then by started_at desc
	sort.SliceStable(topLevel, func(i, j int) bool {
		iRunning := topLevel[i].State == domain.RunStateRunning
		jRunning := topLevel[j].State == domain.RunStateRunning
		if iRunning != jRunning {
			return iRunning
		}
		return topLevel[i].StartedAt.After(topLevel[j].StartedAt)
	})

	// Build attempts list (each top-level run is one attempt).
	var attempts []taskAttemptEntry
	for i, tr := range topLevel {
		attemptNum := len(topLevel) - i
		children := parentMap[tr.ID]
		allInAttempt := append([]*domain.Run{tr}, children...)
		status := taskAggregateStatus(allInAttempt)

		allRuns := append([]*domain.Run{tr}, children...)
		sort.SliceStable(allRuns, func(i, j int) bool {
			return allRuns[i].StartedAt.After(allRuns[j].StartedAt)
		})
		var runsForAttempt []apiRun
		for _, ar := range allRuns {
			runsForAttempt = append(runsForAttempt, toAPIRun(ar, labels))
		}

		attempts = append(attempts, taskAttemptEntry{
			AttemptNum: attemptNum,
			AttemptID:  tr.AttemptID,
			Status:     status,
			StartedAt:  formatTime(tr.StartedAt),
			Runs:       runsForAttempt,
		})
	}

	taskTitle := taskTitles[taskID]
	status := ""
	if len(attempts) > 0 {
		status = attempts[0].Status
	}

	data := map[string]any{
		"Title":    "Task " + taskID,
		"TaskID":   taskID,
		"TaskTitle": taskTitle,
		"Status":   status,
		"Attempts": attempts,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["task_detail"].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *Handler) handleRunsList(w http.ResponseWriter, r *http.Request) {
	projectFilter := r.URL.Query().Get("project")

	// Redirect ?project=<dir> to clean URL /projects/<label>/runs.
	if projectFilter != "" {
		projects, _ := h.store.ListProjects(r.Context())
		labels := projectLabels(projects)
		if label, ok := labels[projectFilter]; ok {
			http.Redirect(w, r, "/projects/"+url.PathEscape(label)+"/runs", http.StatusFound)
			return
		}
	}

	h.renderRunsList(w, r, "")
}

// renderRunsList renders the runs list page, optionally filtered by project directory.
func (h *Handler) renderRunsList(w http.ResponseWriter, r *http.Request, projectFilter string) {
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

	var totalContainerCount int
	for _, c := range containerCounts {
		totalContainerCount += c
	}

	grouped := groupAndSortRuns(runs, labels, h.taskTitlesFromRuns(runs))

	// Build a JSON map of dir→label for the template JS.
	dirToLabel := map[string]string{}
	for _, p := range projectList {
		dirToLabel[p.Dir] = p.Label
	}

	// Resolve the label for the active project filter, if any.
	var projectLabel string
	if projectFilter != "" {
		projectLabel = labels[projectFilter]
	}

	data := map[string]any{
		"Title":               "Runs",
		"GroupedRuns":         grouped,
		"Projects":            projectList,
		"ProjectFilter":       projectFilter,
		"ProjectLabel":        projectLabel,
		"ProjectLabels":       labels,
		"DirToLabel":          dirToLabel,
		"TotalContainerCount": totalContainerCount,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["runs"].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleProjectRuns renders the runs list filtered by the project in the URL path.
func (h *Handler) handleProjectRuns(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	h.renderRunsList(w, r, dir)
}

// stepEntry is a merged view of a step execution for the template.
type stepEntry struct {
	Index        int
	StepName     string
	Result       string
	StartedAt    time.Time
	Duration     string
	AgentName    string
	InputTokens  int64
	OutputTokens int64
	HasUsage     bool
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
		e := stepEntry{
			Index:     len(entries),
			StepName:  c.StepName,
			Result:    c.Result,
			StartedAt: startedAt,
			Duration:  formatDuration(startedAt, c.CompletedAt),
		}
		if c.Usage != nil {
			e.AgentName = c.Usage.AgentName
			e.InputTokens = c.Usage.InputTokens
			e.OutputTokens = c.Usage.OutputTokens
			e.HasUsage = true
		}
		entries = append(entries, e)
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

	// Determine if any step has usage data (for conditional column display).
	var hasUsage bool
	for _, s := range steps {
		if s.HasUsage {
			hasUsage = true
			break
		}
	}

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
		"HasUsage":       hasUsage,
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
	CurrentStep  string `json:"current_step,omitempty"`
	StartedAt    string `json:"started_at"`
	CompletedAt  string `json:"completed_at"`
	Timing       string `json:"timing"`
	ContainerID  string `json:"container_id"`
	ErrorMessage string `json:"error_message"`
	Title        string `json:"title"`
	IsHost       bool   `json:"is_host"`
	ParentRunID  string `json:"parent_run_id,omitempty"`
	TaskID       string `json:"task_id,omitempty"`
}

type apiStep struct {
	StepName     string `json:"step_name"`
	Result       string `json:"result"`
	StartedAt    string `json:"started_at"`
	CompletedAt  string `json:"completed_at"`
	Duration     string `json:"duration"`
	AgentName    string `json:"agent_name,omitempty"`
	InputTokens  int64  `json:"input_tokens,omitempty"`
	OutputTokens int64  `json:"output_tokens,omitempty"`
	HasUsage     bool   `json:"has_usage,omitempty"`
}

type apiRunDetail struct {
	apiRun
	ContainerState string    `json:"container_state"`
	Steps          []apiStep `json:"steps"`
	ChildRuns      []apiRun  `json:"child_runs,omitempty"`
}

func toAPIRun(r *domain.Run, labels map[string]string) apiRun {
	var currentStep string
	if r.State == domain.RunStateRunning && len(r.ActiveSteps) > 0 {
		currentStep = strings.Join(r.ActiveSteps, ", ")
	}
	return apiRun{
		ID:           r.ID,
		WorkflowName: r.WorkflowName,
		ProjectDir:   r.ProjectDir,
		ProjectLabel: labels[r.ProjectDir],
		State:        string(r.State),
		CurrentStep:  currentStep,
		StartedAt:    formatTime(r.StartedAt),
		CompletedAt:  formatTime(r.CompletedAt),
		Timing:       formatRunTiming(r.State, r.StartedAt, r.CompletedAt),
		ContainerID:  r.ContainerID,
		ErrorMessage: r.ErrorMessage,
		Title:        r.Title,
		IsHost:       r.IsHost,
		ParentRunID:  r.ParentRunID,
		TaskID:       r.TaskID,
	}
}

// apiGroupedEntry is a single entry in the grouped runs response.
// Can be a task header, an attempt header, or a run entry.
type apiGroupedEntry struct {
	TaskHeader    bool    `json:"task_header,omitempty"`
	TaskID        string  `json:"task_id,omitempty"`
	TaskTitle     string  `json:"task_title,omitempty"`  // title for task header display
	TaskStatus    string  `json:"task_status,omitempty"` // derived from latest attempt
	AttemptHeader bool    `json:"attempt_header,omitempty"`
	AttemptNum    int     `json:"attempt_num,omitempty"`    // 1-based attempt number (latest first)
	AttemptStatus string  `json:"attempt_status,omitempty"` // state of this attempt's parent run
	AttemptTime   string  `json:"attempt_time,omitempty"`   // when the attempt started
	IsParent      bool    `json:"is_parent,omitempty"`
	IsChild       bool    `json:"is_child,omitempty"`
	Run           *apiRun `json:"run,omitempty"`
}

// taskAggregateStatus computes the aggregate status for the runs within a
// single attempt. Active statuses (running, pending) outweigh terminal ones;
// among terminal runs the worst outcome wins (failed > cancelled > succeeded).
func taskAggregateStatus(runs []*domain.Run) string {
	return string(domain.AttemptAggregateStatus(runs))
}

// groupAndSortRuns filters, sorts, and groups runs by task, mirroring the
// logic previously done client-side. Returns a flat list of grouped entries
// suitable for both HTML template rendering and JSON API responses.
func groupAndSortRuns(runs []*domain.Run, labels map[string]string, taskTitles map[string]string) []apiGroupedEntry {
	// Filter out list-tasks runs
	var filtered []*domain.Run
	for _, r := range runs {
		if r.WorkflowName != "list-tasks" {
			filtered = append(filtered, r)
		}
	}

	// Build lookup and parent→children map
	byID := map[string]*domain.Run{}
	parentMap := map[string][]*domain.Run{}
	var topLevel []*domain.Run

	for _, r := range filtered {
		byID[r.ID] = r
	}
	for _, r := range filtered {
		if r.ParentRunID != "" && byID[r.ParentRunID] != nil {
			parentMap[r.ParentRunID] = append(parentMap[r.ParentRunID], r)
		} else if r.ParentRunID == "" || byID[r.ParentRunID] == nil {
			topLevel = append(topLevel, r)
		}
	}

	// Sort top-level: running first, then by started_at descending
	sort.SliceStable(topLevel, func(i, j int) bool {
		iRunning := topLevel[i].State == domain.RunStateRunning
		jRunning := topLevel[j].State == domain.RunStateRunning
		if iRunning != jRunning {
			return iRunning
		}
		return topLevel[i].StartedAt.After(topLevel[j].StartedAt)
	})

	// Group by task_id preserving sort order, but interleave task groups
	// and ungrouped runs so that running items always appear above completed
	// ones regardless of whether they have a task ID.
	taskGroups := map[string][]*domain.Run{}
	emittedTask := map[string]bool{}

	// Build result by walking topLevel in sorted order. Each task group is
	// emitted as a block the first time we encounter a run belonging to it.
	var result []apiGroupedEntry

	for _, r := range topLevel {
		if r.TaskID != "" {
			taskGroups[r.TaskID] = append(taskGroups[r.TaskID], r)
		}
	}

	for _, r := range topLevel {
		if r.TaskID != "" {
			if emittedTask[r.TaskID] {
				continue
			}
			emittedTask[r.TaskID] = true
			group := taskGroups[r.TaskID]

			// Task status: aggregate of the latest attempt's runs (parent + children).
			latestStatus := ""
			if len(group) > 0 {
				latestChildren := parentMap[group[0].ID]
				latestAttemptRuns := append([]*domain.Run{group[0]}, latestChildren...)
				latestStatus = taskAggregateStatus(latestAttemptRuns)
			}
			result = append(result, apiGroupedEntry{TaskHeader: true, TaskID: r.TaskID, TaskTitle: taskTitles[r.TaskID], TaskStatus: latestStatus})

			// Each top-level run in the group is an attempt.
			// Number them: latest attempt = 1 (shown first due to sort).
			for i, gr := range group {
				attemptNum := len(group) - i
				children := parentMap[gr.ID]
				// Aggregate status from the parent run and all its children so the
				// attempt header reflects the overall state (e.g. running if any
				// child is still running, even after the parent itself has finished).
				allInAttempt := append([]*domain.Run{gr}, children...)
				attemptStatus := taskAggregateStatus(allInAttempt)
				result = append(result, apiGroupedEntry{
					AttemptHeader: true,
					AttemptNum:    attemptNum,
					AttemptStatus: attemptStatus,
					AttemptTime:   formatTime(gr.StartedAt),
					TaskID:        r.TaskID,
				})
				// Show all runs in the attempt sorted by start time, newest first.
				allInRun := append([]*domain.Run{gr}, children...)
				sort.SliceStable(allInRun, func(x, y int) bool {
					return allInRun[x].StartedAt.After(allInRun[y].StartedAt)
				})
				for _, rr := range allInRun {
					ar := toAPIRun(rr, labels)
					result = append(result, apiGroupedEntry{Run: &ar, IsChild: true})
				}
			}
		} else {
			children := parentMap[r.ID]
			ar := toAPIRun(r, labels)
			result = append(result, apiGroupedEntry{Run: &ar, IsParent: len(children) > 0})
			for _, child := range children {
				ac := toAPIRun(child, labels)
				result = append(result, apiGroupedEntry{Run: &ac, IsChild: true})
			}
		}
	}

	// Orphaned children (parent not in current view)
	for _, r := range filtered {
		if r.ParentRunID != "" && byID[r.ParentRunID] == nil {
			ac := toAPIRun(r, labels)
			result = append(result, apiGroupedEntry{Run: &ac, IsChild: true})
		}
	}

	return result
}

// taskTitlesFromRuns builds a task-ID→title map. It first queries the active
// task provider snapshot, then falls back to titles persisted on runs for
// tasks that are no longer in the active loop.
func (h *Handler) taskTitlesFromRuns(runs []*domain.Run) map[string]string {
	titles := map[string]string{}
	if h.taskProvider != nil {
		seen := map[string]bool{}
		for _, r := range runs {
			if r.ProjectDir == "" || seen[r.ProjectDir] {
				continue
			}
			seen[r.ProjectDir] = true
			for _, te := range h.taskProvider.GetLoopTasks(r.ProjectDir) {
				if te.Title != "" {
					titles[te.ID] = te.Title
				}
			}
		}
	}
	// Fall back to titles persisted on run records.
	for _, r := range runs {
		if r.TaskID != "" && titles[r.TaskID] == "" && r.TaskTitle != "" {
			titles[r.TaskID] = r.TaskTitle
		}
	}
	return titles
}

func (h *Handler) handleAPIRuns(w http.ResponseWriter, r *http.Request) {
	projectFilter := r.URL.Query().Get("project")

	// Redirect ?project=<dir> to clean URL /api/projects/<label>/runs.
	if projectFilter != "" {
		projects, _ := h.store.ListProjects(r.Context())
		labels := projectLabels(projects)
		if label, ok := labels[projectFilter]; ok {
			http.Redirect(w, r, "/api/projects/"+url.PathEscape(label)+"/runs", http.StatusFound)
			return
		}
	}

	h.renderAPIRuns(w, r, "")
}

// handleAPIProjectRuns handles GET /api/projects/{name}/runs.
func (h *Handler) handleAPIProjectRuns(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	h.renderAPIRuns(w, r, dir)
}

// renderAPIRuns returns the JSON runs list, optionally filtered by project directory.
func (h *Handler) renderAPIRuns(w http.ResponseWriter, r *http.Request, projectFilter string) {
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

	result := groupAndSortRuns(runs, labels, h.taskTitlesFromRuns(runs))

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
			StepName:     e.StepName,
			Result:       e.Result,
			StartedAt:    formatTime(e.StartedAt),
			Duration:     e.Duration,
			AgentName:    e.AgentName,
			InputTokens:  e.InputTokens,
			OutputTokens: e.OutputTokens,
			HasUsage:     e.HasUsage,
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

	// Fall back to file path conventions.
	// Try v2 path (.cloche/logs/<taskID>/<attemptID>/<workflow>-<step>.log) first,
	// then v2 without prefix (host workflows write <step>.log, not <workflow>-<step>.log),
	// then legacy path (.cloche/<runID>/output/<step>.log).
	legacyOutputDir := filepath.Join(run.ProjectDir, ".cloche", id, "output")
	var searchDirs []struct{ dir, prefix string }
	if run.AttemptID != "" && run.TaskID != "" {
		v2Dir := filepath.Join(run.ProjectDir, ".cloche", "logs", run.TaskID, run.AttemptID)
		searchDirs = append(searchDirs, struct{ dir, prefix string }{v2Dir, run.WorkflowName + "-"})
		searchDirs = append(searchDirs, struct{ dir, prefix string }{v2Dir, ""})
	}
	searchDirs = append(searchDirs, struct{ dir, prefix string }{legacyOutputDir, ""})

	for _, sd := range searchDirs {
		if logType == "llm" {
			llmPath := filepath.Join(sd.dir, sd.prefix+"llm-"+step+".log")
			if data, err := os.ReadFile(llmPath); err == nil && len(data) > 0 {
				writeOutput(data)
				return
			}
		}
		outputPath := filepath.Join(sd.dir, sd.prefix+step+".log")
		if data, err := os.ReadFile(outputPath); err == nil && len(data) > 0 {
			writeOutput(data)
			return
		}
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

	deleted, errs := h.removeContainers(r.Context(), mgr, runs)

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"deleted": deleted}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	json.NewEncoder(w).Encode(resp)
}

// handleAPIDeleteAllContainers mass-deletes all retained/exited containers across all projects.
func (h *Handler) handleAPIDeleteAllContainers(w http.ResponseWriter, r *http.Request) {
	mgr, ok := h.container.(ContainerManager)
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "container management not available"})
		return
	}

	runs, err := h.store.ListRuns(r.Context(), time.Time{})
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": "failed to list runs"})
		return
	}

	deleted, errs := h.removeContainers(r.Context(), mgr, runs)

	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"deleted": deleted}
	if len(errs) > 0 {
		resp["errors"] = errs
	}
	json.NewEncoder(w).Encode(resp)
}

// removeContainers stops and removes retained containers for the given runs,
// updating the ContainerKept flag in the store. It returns the number of
// containers successfully removed and any per-run errors.
func (h *Handler) removeContainers(ctx context.Context, mgr ContainerManager, runs []*domain.Run) (int, []string) {
	var deleted int
	var errors []string
	for _, run := range runs {
		if !run.ContainerKept || run.ContainerID == "" {
			continue
		}
		// Check container is not running
		status, err := mgr.Inspect(ctx, run.ContainerID)
		if err != nil {
			// Container already gone — just clear the flag
			run.ContainerKept = false
			if err := h.store.UpdateRun(ctx, run); err != nil {
				errors = append(errors, fmt.Sprintf("%s: update failed: %v", run.ID, err))
				continue
			}
			deleted++
			continue
		}
		if status.Running {
			// Stop first, then remove
			if err := mgr.Stop(ctx, run.ContainerID); err != nil {
				errors = append(errors, fmt.Sprintf("%s: stop failed: %v", run.ID, err))
				continue
			}
		}
		if err := mgr.Remove(ctx, run.ContainerID); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", run.ID, err))
			continue
		}
		run.ContainerKept = false
		if err := h.store.UpdateRun(ctx, run); err != nil {
			errors = append(errors, fmt.Sprintf("%s: update failed: %v", run.ID, err))
			continue
		}
		deleted++
	}
	return deleted, errors
}

// defaultLogTail is the maximum number of log lines sent on initial page load.
const defaultLogTail = 1000

// fullLogPath returns the path to the full.log for a run.
// For v2 runs (with AttemptID and TaskID set), uses the v2 .cloche/logs/ path.
// For legacy runs, uses .cloche/<runID>/output/full.log.
func fullLogPath(run *domain.Run) string {
	if run.AttemptID != "" && run.TaskID != "" {
		return filepath.Join(run.ProjectDir, ".cloche", "logs", run.TaskID, run.AttemptID, "full.log")
	}
	return filepath.Join(run.ProjectDir, ".cloche", run.ID, "output", "full.log")
}

// handleAPIStream serves an SSE stream of log lines for a run.
// For active runs, it subscribes to the live broadcaster.
// For completed runs, it serves the archived full.log then closes.
// The initial load is capped to the last defaultLogTail lines; a "meta"
// SSE event is sent first when earlier lines were skipped.
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
		// Serve archived full.log (capped to last defaultLogTail lines)
		h.streamFullLog(w, flusher, run, defaultLogTail)
		// Send done event
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", string(run.State))
		flusher.Flush()
		return
	}

	// Active run: subscribe to live broadcaster if it has an active entry.
	// Without IsActive check, SubscribeWithHistory creates an empty
	// subscription that never receives messages, causing the browser to
	// hang with no output.
	if h.logBroadcast != nil && h.logBroadcast.IsActive(id) {
		sub, history := h.logBroadcast.SubscribeWithHistory(id)
		defer h.logBroadcast.Unsubscribe(id, sub)

		// Send historical lines first so the frontend can populate step buffers
		// for steps that already completed before this SSE connection opened.
		for _, line := range history {
			line = parseLLMLogLine(line)
			if line.Type == "" {
				continue
			}
			data, _ := json.Marshal(line)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		flusher.Flush()

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

	// Broadcaster not active for this run — fall back to full.log.
	h.streamFullLog(w, flusher, run, defaultLogTail)
	fmt.Fprintf(w, "event: done\ndata: %s\n\n", string(run.State))
	flusher.Flush()
}

// handleAPILogs serves paginated log lines as JSON for loading earlier output.
// Query params:
//   - end:   exclusive upper bound (visible line index); defaults to total lines
//   - limit: max lines to return; defaults to defaultLogTail
//
// Response: {"lines": [...], "total": N, "start": S, "end": E}
func (h *Handler) handleAPILogs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}

	logPath := fullLogPath(run)
	lines, err := readVisibleLogLines(logPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"lines": []logstream.LogLine{}, "total": 0, "start": 0, "end": 0})
		return
	}

	total := len(lines)

	end := total
	if v := r.URL.Query().Get("end"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= total {
			end = n
		}
	}

	limit := defaultLogTail
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	start := end - limit
	if start < 0 {
		start = 0
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"lines": lines[start:end],
		"total": total,
		"start": start,
		"end":   end,
	})
}

// handleAPIAttemptStream serves an SSE stream of log lines for an attempt.
// For active attempts, it subscribes to the live broadcaster using the host run ID.
// For completed attempts, it serves the archived full.log then closes.
func (h *Handler) handleAPIAttemptStream(w http.ResponseWriter, r *http.Request) {
	attemptID := r.PathValue("id")

	runs, err := h.store.ListRunsFiltered(r.Context(), domain.RunListFilter{AttemptID: attemptID})
	if err != nil || len(runs) == 0 {
		http.Error(w, "attempt not found", http.StatusNotFound)
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

	// Find host run (no parent) — that's the primary orchestration run for the attempt.
	var hostRun *domain.Run
	for _, rr := range runs {
		if rr.ParentRunID == "" {
			hostRun = rr
			break
		}
	}
	if hostRun == nil {
		hostRun = runs[0]
	}

	isComplete := func(rr *domain.Run) bool {
		return rr.State == domain.RunStateSucceeded ||
			rr.State == domain.RunStateFailed ||
			rr.State == domain.RunStateCancelled
	}

	// Check if any run in the attempt is still active.
	var activeRun *domain.Run
	for _, rr := range runs {
		if !isComplete(rr) {
			activeRun = rr
			break
		}
	}

	if activeRun == nil {
		// All runs complete — serve archived full.log using host run for path resolution.
		h.streamFullLog(w, flusher, hostRun, defaultLogTail)
		fmt.Fprintf(w, "event: done\ndata: %s\n\n", string(hostRun.State))
		flusher.Flush()
		return
	}

	// Active attempt — subscribe to broadcaster using the active host run's ID.
	streamRunID := activeRun.ID
	if hostRun != nil && (activeRun.ParentRunID == "" || hostRun.ID == activeRun.ID) {
		streamRunID = hostRun.ID
	}

	if h.logBroadcast != nil && h.logBroadcast.IsActive(streamRunID) {
		sub, history := h.logBroadcast.SubscribeWithHistory(streamRunID)
		defer h.logBroadcast.Unsubscribe(streamRunID, sub)

		for _, line := range history {
			line = parseLLMLogLine(line)
			if line.Type == "" {
				continue
			}
			data, _ := json.Marshal(line)
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		flusher.Flush()

		ctx := r.Context()
		for {
			select {
			case <-ctx.Done():
				return
			case line, ok := <-sub.C:
				if !ok {
					fmt.Fprintf(w, "event: done\ndata: completed\n\n")
					flusher.Flush()
					return
				}
				line = parseLLMLogLine(line)
				if line.Type == "" {
					continue
				}
				data, _ := json.Marshal(line)
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			}
		}
	}

	// Broadcaster not active — fall back to full.log.
	h.streamFullLog(w, flusher, hostRun, defaultLogTail)
	fmt.Fprintf(w, "event: done\ndata: %s\n\n", string(hostRun.State))
	flusher.Flush()
}

// handleAPIAttemptLogs serves paginated log lines for an attempt as JSON.
func (h *Handler) handleAPIAttemptLogs(w http.ResponseWriter, r *http.Request) {
	attemptID := r.PathValue("id")

	runs, err := h.store.ListRunsFiltered(r.Context(), domain.RunListFilter{AttemptID: attemptID})
	if err != nil || len(runs) == 0 {
		http.Error(w, "attempt not found", http.StatusNotFound)
		return
	}

	// Use host run for log path resolution.
	var hostRun *domain.Run
	for _, rr := range runs {
		if rr.ParentRunID == "" {
			hostRun = rr
			break
		}
	}
	if hostRun == nil {
		hostRun = runs[0]
	}

	logPath := fullLogPath(hostRun)
	lines, err := readVisibleLogLines(logPath)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"lines": []logstream.LogLine{}, "total": 0, "start": 0, "end": 0})
		return
	}

	total := len(lines)

	end := total
	if v := r.URL.Query().Get("end"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= total {
			end = n
		}
	}

	limit := defaultLogTail
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}

	start := end - limit
	if start < 0 {
		start = 0
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"lines": lines[start:end],
		"total": total,
		"start": start,
		"end":   end,
	})
}

// logLineRegex parses "[timestamp] [type] content" format from full.log.
var logLineRegex = regexp.MustCompile(`^\[([^\]]+)\] \[([^\]]+)\] (.*)$`)

// readVisibleLogLines reads a full.log file and returns all visible (non-empty-type)
// log lines after parsing and LLM filtering. Each line's StepName is inferred by
// tracking step_started / step_completed status messages in sequence.
func readVisibleLogLines(logPath string) ([]logstream.LogLine, error) {
	f, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var lines []logstream.LogLine
	var currentStep string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 256*1024), 1024*1024) // 1MB max to handle large Claude JSON lines
	for scanner.Scan() {
		line := parseFullLogLine(scanner.Text())
		// Infer step name from sequential status messages so that SSE events
		// sent for completed runs carry the correct step_name and the frontend
		// can route each line to the right per-step panel.
		if line.Type == "status" {
			if after, ok := strings.CutPrefix(line.Content, "step_started: "); ok {
				currentStep = after
				line.StepName = currentStep
			} else if after, ok := strings.CutPrefix(line.Content, "step_completed: "); ok {
				// Content is "step_completed: <step> -> <result>"
				if idx := strings.Index(after, " -> "); idx >= 0 {
					line.StepName = after[:idx]
				} else {
					line.StepName = after
				}
				currentStep = ""
			} else {
				line.StepName = currentStep
			}
		} else {
			line.StepName = currentStep
		}
		line = parseLLMLogLine(line)
		if line.Type == "" {
			continue
		}
		lines = append(lines, line)
	}
	return lines, scanner.Err()
}

// streamFullLog reads the archived full.log file and sends its entries as SSE events.
// LLM-type lines are parsed from raw Claude JSON into human-readable text.
// If tail > 0 and the log has more lines, only the last tail lines are sent and a
// "meta" SSE event is emitted first with total_lines and skipped counts.
func (h *Handler) streamFullLog(w http.ResponseWriter, flusher http.Flusher, run *domain.Run, tail int) {
	logPath := fullLogPath(run)
	lines, err := readVisibleLogLines(logPath)
	if err != nil {
		return
	}

	total := len(lines)
	start := 0
	if tail > 0 && total > tail {
		start = total - tail
	}

	// Send metadata event when earlier lines were skipped
	if start > 0 {
		meta, _ := json.Marshal(map[string]int{"total_lines": total, "skipped": start})
		fmt.Fprintf(w, "event: meta\ndata: %s\n\n", meta)
		flusher.Flush()
	}

	for _, line := range lines[start:] {
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

// apiUsageSummary is the JSON representation of a per-agent usage summary.
type apiUsageSummary struct {
	AgentName    string  `json:"agent_name"`
	InputTokens  int64   `json:"input_tokens"`
	OutputTokens int64   `json:"output_tokens"`
	TotalTokens  int64   `json:"total_tokens"`
	BurnRate     float64 `json:"burn_rate"` // tokens/hour
}

// apiProjectUsage is the JSON response for /api/projects/{name}/usage.
type apiProjectUsage struct {
	BurnRate1h []apiUsageSummary `json:"burn_rate_1h"`
	Totals24h  []apiUsageSummary `json:"totals_24h"`
}

func (h *Handler) handleAPIProjectUsage(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	now := time.Now()

	summaries1h, err := h.store.QueryUsage(r.Context(), ports.UsageQuery{
		ProjectDir: dir,
		Since:      now.Add(-1 * time.Hour),
		Until:      now,
	})
	if err != nil {
		summaries1h = nil
	}

	summaries24h, err := h.store.QueryUsage(r.Context(), ports.UsageQuery{
		ProjectDir: dir,
		Since:      now.Add(-24 * time.Hour),
		Until:      now,
	})
	if err != nil {
		summaries24h = nil
	}

	toAPI := func(ss []domain.UsageSummary) []apiUsageSummary {
		out := make([]apiUsageSummary, len(ss))
		for i, s := range ss {
			out[i] = apiUsageSummary{
				AgentName:    s.AgentName,
				InputTokens:  s.InputTokens,
				OutputTokens: s.OutputTokens,
				TotalTokens:  s.TotalTokens,
				BurnRate:     s.BurnRate,
			}
		}
		return out
	}

	resp := apiProjectUsage{
		BurnRate1h: toAPI(summaries1h),
		Totals24h:  toAPI(summaries24h),
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
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
		Content string        `json:"content"`
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
			content := ""
			if data, err := os.ReadFile(filepath.Join(promptsDir, e.Name())); err == nil {
				content = string(data)
			}
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
			promptFiles = append(promptFiles, promptFile{Path: relPath, Content: content, History: history})
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
		Location  string       `json:"location"`
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
			Location:  "container",
			Steps:     steps,
			Wires:     wires,
			EntryStep: wf.EntryStep,
		})
	}

	// Parse host workflows from host.cloche
	hostData, err := os.ReadFile(filepath.Join(clocheDir, "host.cloche"))
	if err == nil {
		hostWfs, err := dsl.ParseAllForHost(string(hostData))
		if err == nil {
			// Sort host workflow names for consistent ordering
			var hostNames []string
			for name := range hostWfs {
				hostNames = append(hostNames, name)
			}
			sort.Strings(hostNames)

			for _, name := range hostNames {
				hwf := hostWfs[name]
				var steps []apiStepDef
				for _, s := range hwf.Steps {
					steps = append(steps, apiStepDef{
						Name:    s.Name,
						Type:    string(s.Type),
						Results: s.Results,
						Config:  s.Config,
					})
				}
				sort.Slice(steps, func(i, j int) bool { return steps[i].Name < steps[j].Name })

				var wires []apiWire
				for _, wire := range hwf.Wiring {
					wires = append(wires, apiWire{From: wire.From, Result: wire.Result, To: wire.To})
				}

				workflows = append(workflows, apiWorkflow{
					Name:      hwf.Name,
					File:      filepath.Join(".cloche", "host.cloche"),
					Location:  "host",
					Steps:     steps,
					Wires:     wires,
					EntryStep: hwf.EntryStep,
				})
			}
		}
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

	// Find the workflow file (container workflows first, then host)
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
	// Try host workflows if not found in container workflows
	if wf == nil {
		hostData, err := os.ReadFile(filepath.Join(clocheDir, "host.cloche"))
		if err == nil {
			hostWfs, err := dsl.ParseAllForHost(string(hostData))
			if err == nil {
				wf = hostWfs[workflowName]
			}
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
	if run := step.Config["run"]; run != "" {
		// For file() references, only serve content from .cloche/scripts/ text files
		if strings.HasPrefix(run, `file("`) && strings.HasSuffix(run, `")`) {
			refPath := run[6 : len(run)-2]
			cleanRef := filepath.Clean(refPath)
			scriptsPrefix := filepath.Join(".cloche", "scripts") + string(filepath.Separator)
			if strings.HasPrefix(cleanRef, scriptsPrefix) {
				content, err := resolveFileRef(run, dir)
				if err == nil && isTextContent([]byte(content)) {
					w.Write([]byte(content))
					return
				}
			}
			return
		}
		// Bare command: only serve script content from .cloche/scripts/
		if scriptContent := readScriptFromCommand(run, dir); scriptContent != "" {
			w.Write([]byte(scriptContent))
			return
		}
		return
	}
	if wfName := step.Config["workflow_name"]; wfName != "" {
		w.Write([]byte("Dispatches workflow: " + wfName))
		return
	}

	http.Error(w, "no content available", http.StatusNotFound)
}

// handleAPITasks returns the task pipeline state for a project's orchestration loop.
func (h *Handler) handleAPITasks(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	if h.taskProvider == nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode([]any{})
		return
	}

	tasks := h.taskProvider.GetLoopTasks(dir)
	if tasks == nil {
		tasks = []TaskEntry{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

// handleAPIAllTasks returns a JSON task summary list derived from all runs.
func (h *Handler) handleAPIAllTasks(w http.ResponseWriter, r *http.Request) {
	runs, err := h.store.ListRuns(r.Context(), time.Time{})
	if err != nil {
		http.Error(w, "failed to list runs", http.StatusInternalServerError)
		return
	}
	projects, _ := h.store.ListProjects(r.Context())
	labels := projectLabels(projects)
	taskTitles := h.taskTitlesFromRuns(runs)
	tasks := buildTaskSummaries(runs, labels, taskTitles)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
}

// handleAPIReleaseTask releases a stale claimed task back to open status.
func (h *Handler) handleAPIReleaseTask(w http.ResponseWriter, r *http.Request) {
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

	if err := h.taskProvider.ReleaseTask(r.Context(), dir, taskID); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to release task: %v", err)})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// --- Failed tasks dashboard ---

// failedOpenTaskEntry holds a summary for the failed-but-still-open tasks dashboard.
type failedOpenTaskEntry struct {
	TaskID       string `json:"task_id"`
	TaskTitle    string `json:"task_title,omitempty"`
	ProjectLabel string `json:"project_label,omitempty"`
	FailedCount  int    `json:"failed_count"`
	LatestRunID  string `json:"latest_run_id"`
	LatestError  string `json:"latest_error,omitempty"`
	LatestTime   string `json:"latest_time,omitempty"`
	OpenInBead   bool   `json:"open_in_bead"`
}

// buildFailedOpenTasks returns all tasks that have failed runs but have not yet
// succeeded. For each task the latest top-level run determines recency. If a
// taskProvider is configured, tasks that appear in the live bead task list are
// flagged as open in bead.
func (h *Handler) buildFailedOpenTasks(ctx context.Context) []failedOpenTaskEntry {
	runs, err := h.store.ListRuns(ctx, time.Time{})
	if err != nil {
		return nil
	}

	projects, _ := h.store.ListProjects(ctx)
	labels := projectLabels(projects)
	taskTitles := h.taskTitlesFromRuns(runs)

	// Build bead open-task set across all projects (keyed by task ID).
	beadOpen := map[string]bool{}
	if h.taskProvider != nil {
		seen := map[string]bool{}
		for _, r := range runs {
			if r.ProjectDir == "" || seen[r.ProjectDir] {
				continue
			}
			seen[r.ProjectDir] = true
			for _, te := range h.taskProvider.GetLoopTasks(r.ProjectDir) {
				beadOpen[te.ID] = true
			}
		}
	}

	// Build parent→children map and collect top-level runs.
	byID := map[string]*domain.Run{}
	for _, r := range runs {
		byID[r.ID] = r
	}
	parentMap := map[string][]*domain.Run{}
	var topLevel []*domain.Run
	for _, r := range runs {
		if r.WorkflowName == "list-tasks" {
			continue
		}
		if r.ParentRunID != "" && byID[r.ParentRunID] != nil {
			parentMap[r.ParentRunID] = append(parentMap[r.ParentRunID], r)
		} else {
			topLevel = append(topLevel, r)
		}
	}

	// Group top-level runs by task ID (only tasks with a task ID).
	taskGroups := map[string][]*domain.Run{}
	taskOrder := []string{}
	seenTask := map[string]bool{}

	// Sort: most recently started first.
	sort.SliceStable(topLevel, func(i, j int) bool {
		return topLevel[i].StartedAt.After(topLevel[j].StartedAt)
	})

	for _, r := range topLevel {
		if r.TaskID == "" {
			continue
		}
		taskGroups[r.TaskID] = append(taskGroups[r.TaskID], r)
		if !seenTask[r.TaskID] {
			seenTask[r.TaskID] = true
			taskOrder = append(taskOrder, r.TaskID)
		}
	}

	var result []failedOpenTaskEntry
	for _, tid := range taskOrder {
		group := taskGroups[tid]
		if len(group) == 0 {
			continue
		}
		// Determine overall task status from the latest attempt.
		latestRun := group[0]
		latestChildren := parentMap[latestRun.ID]
		latestAttemptRuns := append([]*domain.Run{latestRun}, latestChildren...)
		status := taskAggregateStatus(latestAttemptRuns)

		// Only include tasks whose latest attempt is failed (not succeeded or running).
		if status != "failed" {
			continue
		}

		// Count how many attempts failed.
		var failedCount int
		for _, r := range group {
			children := parentMap[r.ID]
			allInAttempt := append([]*domain.Run{r}, children...)
			if taskAggregateStatus(allInAttempt) == "failed" {
				failedCount++
			}
		}

		result = append(result, failedOpenTaskEntry{
			TaskID:       tid,
			TaskTitle:    taskTitles[tid],
			ProjectLabel: labels[latestRun.ProjectDir],
			FailedCount:  failedCount,
			LatestRunID:  latestRun.ID,
			LatestError:  latestRun.ErrorMessage,
			LatestTime:   formatTime(latestRun.StartedAt),
			OpenInBead:   beadOpen[tid],
		})
	}
	return result
}

// handleFailedTasksDashboard renders the failed-but-still-open tasks dashboard.
func (h *Handler) handleFailedTasksDashboard(w http.ResponseWriter, r *http.Request) {
	tasks := h.buildFailedOpenTasks(r.Context())
	if tasks == nil {
		tasks = []failedOpenTaskEntry{}
	}
	hasBeadProvider := h.taskProvider != nil
	data := map[string]any{
		"Title":           "Failed Open Tasks",
		"Tasks":           tasks,
		"HasBeadProvider": hasBeadProvider,
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["failed_tasks"].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleAPIFailedTasks returns a JSON list of failed-but-still-open tasks.
func (h *Handler) handleAPIFailedTasks(w http.ResponseWriter, r *http.Request) {
	tasks := h.buildFailedOpenTasks(r.Context())
	if tasks == nil {
		tasks = []failedOpenTaskEntry{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tasks)
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

// readScriptFromCommand tries to find a script file referenced in a run command.
// For commands like "bash .cloche/scripts/setup.sh arg1", it extracts the file
// path and reads its contents. Returns empty string if no script file is found.
func readScriptFromCommand(command, baseDir string) string {
	scriptsPrefix := filepath.Join(".cloche", "scripts") + string(filepath.Separator)
	fields := strings.Fields(command)
	for _, field := range fields {
		// Skip flags (e.g. -x, --verbose)
		if strings.HasPrefix(field, "-") {
			continue
		}
		// Skip shell redirections and pipes
		if field == "2>&1" || field == ">" || field == ">>" || field == "|" {
			continue
		}
		// Only read files under .cloche/scripts/
		cleanField := filepath.Clean(field)
		if !strings.HasPrefix(cleanField, scriptsPrefix) {
			continue
		}
		absPath := filepath.Join(baseDir, field)
		info, err := os.Stat(absPath)
		if err == nil && !info.IsDir() {
			data, err := os.ReadFile(absPath)
			if err == nil && isTextContent(data) {
				return string(data)
			}
		}
	}
	return ""
}

// isTextContent checks if data appears to be text (no null bytes in first 8KB).
func isTextContent(data []byte) bool {
	limit := 8192
	if len(data) < limit {
		limit = len(data)
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return false
		}
	}
	return true
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

// jsonMap serializes a map to a JSON string for embedding in templates.
func jsonMap(m map[string]string) template.JS {
	b, _ := json.Marshal(m)
	return template.JS(b)
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
