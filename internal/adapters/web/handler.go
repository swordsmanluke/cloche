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

	"github.com/cloche-dev/cloche/internal/attention"
	"github.com/cloche-dev/cloche/internal/builtin"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/intent"
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

// WithTaskStore sets the task store used to resolve Task/Attempt records
// (title, source, attempt ordinal) for the task-stack API. Optional; when
// unset, the task-stack endpoint falls back to per-run data only.
func WithTaskStore(ts ports.TaskStore) HandlerOption {
	return func(h *Handler) { h.taskStore = ts }
}

// WithAttentionProvider sets the provider used to compute the "Needs you"
// attention set (see internal/attention).
func WithAttentionProvider(ap AttentionProvider) HandlerOption {
	return func(h *Handler) { h.attentionProvider = ap }
}

// WithAttentionMuter sets the provider used to mute a "Needs you" item.
func WithAttentionMuter(am AttentionMuter) HandlerOption {
	return func(h *Handler) { h.attentionMuter = am }
}

// WithOccupancyProvider sets the provider for querying orchestration loop
// concurrency-slot occupancy (slots/queued/polls and the all-projects summary).
func WithOccupancyProvider(op OccupancyProvider) HandlerOption {
	return func(h *Handler) { h.occupancyProvider = op }
}

// WithOrchestrateFunc sets the function used to trigger the orchestration loop for a project.
func WithOrchestrateFunc(fn func(ctx context.Context, projectDir string) (int, error)) HandlerOption {
	return func(h *Handler) { h.orchestrateFn = fn }
}

// WithLoopStatusFunc sets the function used to check whether the orchestration loop is running.
func WithLoopStatusFunc(fn func(projectDir string) bool) HandlerOption {
	return func(h *Handler) { h.loopStatusFn = fn }
}

// WithStopLoopFunc sets the function used to stop the orchestration loop for a project.
func WithStopLoopFunc(fn func(ctx context.Context, projectDir string) error) HandlerOption {
	return func(h *Handler) { h.stopLoopFn = fn }
}

// WithStopRunFunc sets the function used to stop all active runs for a task.
func WithStopRunFunc(fn func(ctx context.Context, taskID string) error) HandlerOption {
	return func(h *Handler) { h.stopRunFn = fn }
}

// WithScanFunc sets the function used to dispatch an intent-scan for a project,
// returning the dispatched run ID.
func WithScanFunc(fn func(ctx context.Context, projectDir string) (string, error)) HandlerOption {
	return func(h *Handler) { h.scanFn = fn }
}

// WithActivityStore sets the store backing the activity ticker/stream API
// (see handler_activity.go). Optional; when unset, the endpoint returns an
// empty stream.
func WithActivityStore(as ports.ActivityStore) HandlerOption {
	return func(h *Handler) { h.activityStore = as }
}

// WithGetThreadFunc sets the function used to resolve a help-thread address
// (or bare thread ID) to its summary and full message transcript, backing
// the parked-run pane (see handler_thread.go). Optional; when unset, the
// thread endpoint reports the help channel as unavailable.
func WithGetThreadFunc(fn GetThreadFunc) HandlerOption {
	return func(h *Handler) { h.getThreadFn = fn }
}

// WithReplyThreadFunc sets the function used to post a reply to a help
// thread. Must go through the same path as `cloche threads reply` —
// including resuming a parked run — so cmd/cloched wires this directly to
// the daemon's ReplyThread RPC handler rather than reimplementing it.
// Optional; when unset, the reply endpoint reports the help channel as
// unavailable.
func WithReplyThreadFunc(fn ReplyThreadFunc) HandlerOption {
	return func(h *Handler) { h.replyThreadFn = fn }
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
	// CloseTask runs the project's close/cancel task contract for taskID —
	// see host.ResolveCloseTaskWorkflow. Returns an error satisfying
	// errors.Is(err, host.ErrNoCloseContract) when the project defines
	// neither workflow, which the handler surfaces as "not available" rather
	// than a failure.
	CloseTask(ctx context.Context, projectDir string, taskID string) error
	// RunOnce dispatches a single attempt of workflowName for taskID outside
	// the orchestration loop, returning the new run's ID.
	RunOnce(ctx context.Context, projectDir, taskID, workflowName, prompt string) (string, error)
}

// AttentionProvider computes the "Needs you" attention set for a project.
type AttentionProvider interface {
	AttentionItems(ctx context.Context, projectDir string) ([]attention.Item, error)
}

// AttentionMuter suppresses a "Needs you" item (identified by
// attention.Item.Key) from future attention computations.
type AttentionMuter interface {
	MuteAttentionItem(ctx context.Context, projectDir, key string) error
}

// OccupancySlot describes a single busy concurrency slot: an in-flight host
// run currently occupying it.
type OccupancySlot struct {
	Index       int    `json:"index"`
	RunID       string `json:"run_id"`
	TaskID      string `json:"task_id,omitempty"`
	AttemptID   string `json:"attempt_id,omitempty"`
	CurrentStep string `json:"current_step,omitempty"`
	StartedAt   string `json:"started_at"`
}

// QueuedItem describes a task or run waiting for a concurrency slot. Reason
// is "capacity" (an open task discovered but no free slot yet) or "resuming"
// (a run that finished a poll step and is waiting to reacquire its slot).
type QueuedItem struct {
	TaskID string `json:"task_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	Reason string `json:"reason"`
	Since  string `json:"since"`
}

// PollItem describes a poll step currently being driven asynchronously —
// parked, not holding a concurrency slot.
type PollItem struct {
	RunID      string `json:"run_id"`
	Step       string `json:"step"`
	LastPollAt string `json:"last_poll_at"`
	PollCount  int    `json:"poll_count"`
}

// LoopOccupancy summarizes a project's orchestration loop concurrency-slot
// usage for the GET /api/projects/{name}/loop/occupancy endpoint.
type LoopOccupancy struct {
	MaxConcurrency int             `json:"max_concurrency"`
	Slots          []OccupancySlot `json:"slots"`
	Queued         []QueuedItem    `json:"queued"`
	Polls          []PollItem      `json:"polls"`
}

// ProjectOccupancy is a cheap per-project rollup for a dashboard tab bar.
// AttentionCount is always 0 until the attention-model ticket lands.
type ProjectOccupancy struct {
	ProjectDir     string `json:"project_dir"`
	Name           string `json:"name"`
	Running        int    `json:"running"`
	Queued         int    `json:"queued"`
	Health         string `json:"health"`
	AttentionCount int    `json:"attention_count"`
}

// OccupancyProvider retrieves loop occupancy data across projects, backed by
// the daemon's in-memory orchestration loops. Named distinctly from the
// GetLoopOccupancy/ListLoopOccupancy gRPC RPCs (same underlying data, but a
// gRPC server implementation can't share method names with this interface).
type OccupancyProvider interface {
	// LoopOccupancySnapshot returns the occupancy detail for a single
	// project's orchestration loop. ok is false when no loop is active.
	LoopOccupancySnapshot(projectDir string) (occupancy LoopOccupancy, ok bool)
	// AllLoopOccupancy returns the per-project summary across all projects.
	AllLoopOccupancy() []ProjectOccupancy
}

type Handler struct {
	store             ports.RunStore
	captures          ports.CaptureStore
	logStore          ports.LogStore
	container         ContainerLogger
	logBroadcast      *logstream.Broadcaster
	taskProvider      TaskProvider
	taskStore         ports.TaskStore
	activityStore     ports.ActivityStore
	attentionProvider AttentionProvider
	attentionMuter    AttentionMuter
	occupancyProvider OccupancyProvider
	orchestrateFn     func(ctx context.Context, projectDir string) (int, error)
	loopStatusFn      func(projectDir string) bool
	stopLoopFn        func(ctx context.Context, projectDir string) error
	stopRunFn         func(ctx context.Context, taskID string) error
	scanFn            func(ctx context.Context, projectDir string) (string, error)
	getThreadFn       GetThreadFunc   // resolves a help thread for the parked-run pane
	replyThreadFn     ReplyThreadFunc // posts a reply to a help thread; same path as `cloche threads reply`
	mcpSecret         []byte          // enables /mcp when non-empty; see WithHelpMCP
	askHelpFn         AskHelpFunc     // handles ask_user tool calls on /mcp
	pages             map[string]*template.Template
	mux               *http.ServeMux
}

// NewHandler creates a web dashboard handler.
func NewHandler(store ports.RunStore, captures ports.CaptureStore, opts ...HandlerOption) (*Handler, error) {
	funcMap := template.FuncMap{
		"clocheVersion": version.Version,
	}

	base, err := template.New("").Funcs(funcMap).ParseFS(content, "templates/layout.html")
	if err != nil {
		return nil, fmt.Errorf("parse layout: %w", err)
	}

	pages := map[string]*template.Template{}
	for _, page := range []string{"console"} {
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

	h.mux.HandleFunc("GET /{$}", h.handleLegacyRoot)
	h.mux.HandleFunc("GET /projects/{name}/runs", h.handleLegacyProjectRedirect)
	h.mux.HandleFunc("GET /runs", h.handleLegacyRunsRedirect)
	h.mux.HandleFunc("GET /runs/{id}", h.handleLegacyRunRedirect)
	h.mux.HandleFunc("GET /projects/{name}", h.handleLegacyProjectRedirect)
	h.mux.HandleFunc("GET /{name}", h.handleConsoleShell)
	h.mux.HandleFunc("GET /{name}/{taskID...}", h.handleConsoleShell)
	h.mux.HandleFunc("GET /api/projects", h.handleAPIProjects)
	h.mux.HandleFunc("GET /api/projects/{name}/runs", h.handleAPIProjectRuns)
	h.mux.HandleFunc("GET /api/runs", h.handleAPIRuns)
	h.mux.HandleFunc("GET /api/runs/{id}", h.handleAPIRunDetail)
	h.mux.HandleFunc("GET /api/runs/{id}/thread", h.handleAPIRunThread)
	h.mux.HandleFunc("POST /api/runs/{id}/thread/reply", h.handleAPIRunThreadReply)
	h.mux.HandleFunc("GET /api/runs/{id}/steps/{step}/output", h.handleAPIStepOutput)
	h.mux.HandleFunc("POST /api/runs/{id}/stop", h.handleAPIStopRun)
	h.mux.HandleFunc("DELETE /api/runs/{id}/container", h.handleAPIDeleteContainer)
	h.mux.HandleFunc("GET /api/runs/{id}/branch", h.handleAPIRunBranch)
	h.mux.HandleFunc("GET /api/runs/{id}/diff", h.handleAPIRunDiff)
	h.mux.HandleFunc("GET /api/runs/{id}/console", h.handleAPIRunConsole)
	h.mux.HandleFunc("GET /api/projects/{name}/containers", h.handleAPIProjectContainers)
	h.mux.HandleFunc("DELETE /api/projects/{name}/containers", h.handleAPIDeleteProjectContainers)
	h.mux.HandleFunc("DELETE /api/containers", h.handleAPIDeleteAllContainers)
	h.mux.HandleFunc("GET /api/projects/{name}/usage", h.handleAPIProjectUsage)
	h.mux.HandleFunc("GET /api/projects/{name}/info/prompt-diff", h.handleAPIPromptDiff)
	h.mux.HandleFunc("GET /api/projects/{name}/workflows", h.handleAPIWorkflows)
	h.mux.HandleFunc("GET /api/projects/{name}/workflows/{workflow}/steps/{step}/content", h.handleAPIStepContent)
	h.mux.HandleFunc("GET /api/projects/{name}/tasks", h.handleAPITasks)
	h.mux.HandleFunc("GET /api/projects/{name}/tasks/stack", h.handleAPITaskStack)
	h.mux.HandleFunc("GET /api/projects/{name}/tasks/{taskId}/attempts", h.handleAPITaskAttempts)
	h.mux.HandleFunc("GET /api/activity", h.handleAPIActivity)
	h.mux.HandleFunc("POST /api/projects/{name}/tasks/{taskId}/release", h.handleAPIReleaseTask)
	h.mux.HandleFunc("POST /api/projects/{name}/tasks/{taskId}/close", h.handleAPICloseTask)
	h.mux.HandleFunc("POST /api/projects/{name}/tasks/{taskId}/run-once", h.handleAPIRunOnce)
	h.mux.HandleFunc("POST /api/projects/{name}/attention/mute", h.handleAPIMuteAttention)
	h.mux.HandleFunc("POST /api/projects/{name}/trigger", h.handleAPITriggerOrchestrator)
	h.mux.HandleFunc("GET /api/projects/{name}/loop/status", h.handleAPILoopStatus)
	h.mux.HandleFunc("POST /api/projects/{name}/loop/stop", h.handleAPILoopStop)
	h.mux.HandleFunc("GET /api/projects/{name}/loop/occupancy", h.handleAPILoopOccupancy)
	h.mux.HandleFunc("GET /api/projects/occupancy", h.handleAPIProjectsOccupancy)
	h.mux.HandleFunc("GET /api/projects/{name}/intent/requirements", h.handleAPIIntentRequirementsList)
	h.mux.HandleFunc("PATCH /api/projects/{name}/intent/requirements", h.handleAPIIntentRequirementsPatch)
	h.mux.HandleFunc("GET /api/projects/{name}/intent/domains", h.handleAPIIntentDomainsGet)
	h.mux.HandleFunc("PUT /api/projects/{name}/intent/domains", h.handleAPIIntentDomainsPut)
	h.mux.HandleFunc("POST /api/projects/{name}/intent/scan", h.handleAPIIntentScan)
	h.mux.HandleFunc("GET /api/projects/{name}/intent/doc", h.handleAPIIntentDoc)
	h.mux.HandleFunc("GET /api/runs/{id}/logs", h.handleAPILogs)
	h.mux.HandleFunc("GET /api/runs/{id}/stream", h.handleAPIStream)
	h.mux.HandleFunc("GET /api/attempts/{id}/stream", h.handleAPIAttemptStream)
	h.mux.HandleFunc("GET /api/attempts/{id}/logs", h.handleAPIAttemptLogs)
	h.mux.HandleFunc("GET /tasks/{taskID}", h.handleLegacyTaskRedirect)
	h.mux.HandleFunc("GET /failed-tasks", h.handleLegacyFailedTasksRedirect)
	h.mux.HandleFunc("GET /api/failed-tasks", h.handleAPIFailedTasks)
	h.mux.HandleFunc("POST /mcp", h.handleMCP)
	h.mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticFS)))

	return h, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// --- HTML handlers ---

const healthWindowSize = 10

// stepEntry is a merged view of a step execution for the template.
type stepEntry struct {
	Index        int
	StepName     string
	Result       string
	StartedAt    time.Time
	CompletedAt  time.Time
	Duration     string
	AgentName    string
	InputTokens  int64
	OutputTokens int64
	HasUsage     bool
	// Tree fields
	RunID       string // run that owns this step (may differ from page run for sub-steps)
	Depth       int    // 0 = top-level, 1 = child run step, etc.
	IsWorkflow  bool   // true if a child run was spawned by this step
	ParentIndex int    // flat-list index of the parent workflow step (-1 for top-level)
}

// flattenRun builds a flat list of step entries for the run detail view. It
// inserts child-run steps immediately after the workflow step that spawned them,
// indented at depth+1. Steps from the top-level run are at depth 0.
func flattenRun(run *domain.Run, topCaps []*domain.StepExecution, childRuns []*domain.Run, childCaps map[string][]*domain.StepExecution) []stepEntry {
	topSteps := mergeCaptures(topCaps)

	// Build a mapping: parent step name → child run.
	// Prefer the new ParentStepName field; fall back to matching the child
	// run's workflow name against the step name (legacy runs).
	stepToChild := map[string]*domain.Run{}
	for _, c := range childRuns {
		if c.ParentStepName != "" {
			stepToChild[c.ParentStepName] = c
		}
	}
	// Fallback: for child runs that still have no entry, match by workflow name.
	for _, c := range childRuns {
		if c.ParentStepName == "" {
			if _, exists := stepToChild[c.WorkflowName]; !exists {
				stepToChild[c.WorkflowName] = c
			}
		}
	}

	var result []stepEntry
	for _, s := range topSteps {
		child := stepToChild[s.StepName]
		s.RunID = run.ID
		s.Depth = 0
		s.ParentIndex = -1
		s.IsWorkflow = child != nil
		s.Index = len(result)
		result = append(result, s)

		if child != nil {
			parentIdx := len(result) - 1
			childSteps := mergeCaptures(childCaps[child.ID])
			for _, cs := range childSteps {
				cs.RunID = child.ID
				cs.Depth = 1
				cs.ParentIndex = parentIdx
				cs.Index = len(result)
				result = append(result, cs)
			}
		}
	}
	return result
}

// matchChildRun finds the child run spawned by parentStep, marking it consumed
// so repeated workflow steps don't all match the same child.
//
// Primary match: child.ParentStepName == parentStep.StepName.
// Fallback (legacy runs where ParentStepName is empty): child.WorkflowName ==
// parentStep.StepName AND child.StartedAt falls within
// [parentStep.StartedAt, parentStep.CompletedAt].  The earliest unmatched
// child is chosen.
func matchChildRun(parentStep stepEntry, children []*domain.Run, consumed map[string]bool) *domain.Run {
	// Primary: explicit parent step name linkage.
	for _, c := range children {
		if consumed[c.ID] {
			continue
		}
		if c.ParentStepName != "" && c.ParentStepName == parentStep.StepName {
			consumed[c.ID] = true
			return c
		}
	}

	// Fallback: legacy runs without ParentStepName.
	var best *domain.Run
	for _, c := range children {
		if consumed[c.ID] || c.ParentStepName != "" {
			continue
		}
		if c.WorkflowName != parentStep.StepName {
			continue
		}
		// Check time range if both bounds are known.
		if !parentStep.StartedAt.IsZero() && !c.StartedAt.IsZero() {
			if c.StartedAt.Before(parentStep.StartedAt) {
				continue
			}
			if !parentStep.CompletedAt.IsZero() && c.StartedAt.After(parentStep.CompletedAt) {
				continue
			}
		}
		if best == nil || c.StartedAt.Before(best.StartedAt) {
			best = c
		}
	}
	if best != nil {
		consumed[best.ID] = true
	}
	return best
}

// flattenRun (method) is the recursive variant of the package-level flattenRun.
// It fetches its own captures and child runs from the stores, then builds a
// flat []apiStep list where child-run steps are inlined immediately after
// the workflow step that spawned them, at depth+1.
func (h *Handler) flattenRun(ctx context.Context, runID string, depth, parentIdx int) ([]apiStep, error) {
	return h.flattenRunFrom(ctx, runID, depth, parentIdx, 0)
}

// flattenRunFrom is the inner recursive helper for flattenRun. baseIdx is the
// global starting index for steps produced by this invocation; it ensures that
// Index values are globally unique across all levels of recursion.
func (h *Handler) flattenRunFrom(ctx context.Context, runID string, depth, parentIdx, baseIdx int) ([]apiStep, error) {
	caps, _ := h.captures.GetCaptures(ctx, runID)
	childRuns, _ := h.store.ListChildRuns(ctx, runID)

	steps := mergeCaptures(caps)
	consumed := make(map[string]bool)

	var result []apiStep
	for _, s := range steps {
		child := matchChildRun(s, childRuns, consumed)
		idx := baseIdx + len(result)
		as := apiStep{
			StepName:     s.StepName,
			Result:       s.Result,
			StartedAt:    formatTime(s.StartedAt),
			CompletedAt:  formatTime(s.CompletedAt),
			Duration:     s.Duration,
			AgentName:    s.AgentName,
			InputTokens:  s.InputTokens,
			OutputTokens: s.OutputTokens,
			HasUsage:     s.HasUsage,
			Index:        idx,
			RunID:        runID,
			Depth:        depth,
			ParentIndex:  parentIdx,
			IsWorkflow:   child != nil,
		}
		if child != nil {
			as.ChildRunID = child.ID
			as.ChildState = string(child.State)
		}
		if pollStore, ok := h.store.(ports.PollStore); ok {
			if rec, err := pollStore.GetPoll(ctx, runID, s.StepName); err == nil && rec != nil {
				as.LastPollAt = formatTime(rec.LastPollAt)
				as.PollCount = rec.PollCount
			}
		}
		result = append(result, as)

		if child != nil {
			newBase := baseIdx + len(result)
			subSteps, _ := h.flattenRunFrom(ctx, child.ID, depth+1, idx, newBase)
			result = append(result, subSteps...)
		}
	}
	return result, nil
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
			Index:       len(entries),
			StepName:    c.StepName,
			Result:      c.Result,
			StartedAt:   startedAt,
			CompletedAt: c.CompletedAt,
			Duration:    formatDuration(startedAt, c.CompletedAt),
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
	// Tree fields
	Index       int    `json:"index"`
	RunID       string `json:"run_id"`
	Depth       int    `json:"depth"`
	IsWorkflow  bool   `json:"is_workflow,omitempty"`
	ParentIndex int    `json:"parent_index"`
	ChildRunID  string `json:"child_run_id,omitempty"`
	ChildState  string `json:"child_state,omitempty"`
	// Poll fields, set only while the step is an active poll step (see
	// ports.PollStore) — for the step strip's "last poll time and count".
	LastPollAt string `json:"last_poll_at,omitempty"`
	PollCount  int    `json:"poll_count,omitempty"`
}

// apiAgentTokens is one agent's token totals, aggregated across a run's
// (and its inlined child runs') steps for the facts row's "tokens" fact.
type apiAgentTokens struct {
	AgentName    string `json:"agent_name"`
	InputTokens  int64  `json:"input_tokens"`
	OutputTokens int64  `json:"output_tokens"`
}

// aggregateTokenUsage sums input/output tokens per agent across steps
// (including inlined child-run steps), preserving first-seen agent order.
func aggregateTokenUsage(steps []apiStep) []apiAgentTokens {
	var order []string
	totals := map[string]*apiAgentTokens{}
	for _, s := range steps {
		if !s.HasUsage || s.AgentName == "" {
			continue
		}
		t, ok := totals[s.AgentName]
		if !ok {
			t = &apiAgentTokens{AgentName: s.AgentName}
			totals[s.AgentName] = t
			order = append(order, s.AgentName)
		}
		t.InputTokens += s.InputTokens
		t.OutputTokens += s.OutputTokens
	}
	result := make([]apiAgentTokens, 0, len(order))
	for _, name := range order {
		result = append(result, *totals[name])
	}
	return result
}

// shortSHA truncates a git SHA to its short (7-char) form for display.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

type apiRunDetail struct {
	apiRun
	ContainerState string           `json:"container_state"`
	Steps          []apiStep        `json:"steps"`
	ChildRuns      []apiRun         `json:"child_runs,omitempty"`
	TokenUsage     []apiAgentTokens `json:"token_usage,omitempty"`
	PromptFile     string           `json:"prompt_file,omitempty"`
	GitRevision    string           `json:"git_revision,omitempty"`
	// ParkedTitle and ParkedSeconds are set when State == "parked" (a run
	// awaiting a help-thread reply): the question's title and how long ago
	// the parked step completed. Backs the parked-run pane's header.
	ParkedTitle   string `json:"parked_title,omitempty"`
	ParkedSeconds int64  `json:"parked_seconds,omitempty"`
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

	// Built-in workflow runs (e.g. the automatic intent-scan trigger) carry a
	// synthetic task ID but aren't user work items — treated as ungrouped so
	// they render as standalone runs instead of masquerading as a task.
	hasTaskGroup := func(r *domain.Run) bool { return r.TaskID != "" && !r.IsBuiltin }

	for _, r := range topLevel {
		if hasTaskGroup(r) {
			taskGroups[r.TaskID] = append(taskGroups[r.TaskID], r)
		}
	}

	for _, r := range topLevel {
		if hasTaskGroup(r) {
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

	// Redirect ?project=<dir> to clean URL /api/projects/<slug>/runs.
	if projectFilter != "" {
		projects, _ := h.store.ListProjects(r.Context())
		slugs := projectSlugs(projects)
		if slug, ok := slugs[projectFilter]; ok {
			http.Redirect(w, r, "/api/projects/"+url.PathEscape(slug)+"/runs", http.StatusFound)
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
	slugs := projectSlugs(projects)

	// Optional ?project=<dir> filters the result to a single project, letting
	// callers (e.g. `cloche health`) send the directory they already know
	// instead of computing a slug/label client-side.
	if projectFilter := r.URL.Query().Get("project"); projectFilter != "" {
		var filtered []string
		for _, dir := range projects {
			if dir == projectFilter {
				filtered = append(filtered, dir)
				break
			}
		}
		projects = filtered
	}

	type apiHealth struct {
		Status string `json:"status"`
		Passed int    `json:"passed"`
		Failed int    `json:"failed"`
		Total  int    `json:"total"`
	}
	type apiProject struct {
		Dir            string    `json:"dir"`
		Label          string    `json:"label"`
		Slug           string    `json:"slug"`
		Health         apiHealth `json:"health"`
		ActiveCount    int       `json:"active_count"`
		AttentionCount int       `json:"attention_count"`
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
		var attentionCount int
		if h.attentionProvider != nil {
			if items, err := h.attentionProvider.AttentionItems(r.Context(), dir); err == nil {
				attentionCount = len(items)
			}
		}
		result[i] = apiProject{
			Dir:   dir,
			Label: labels[dir],
			Slug:  slugs[dir],
			Health: apiHealth{
				Status: string(health.Status),
				Passed: health.Passed,
				Failed: health.Failed,
				Total:  health.Total,
			},
			ActiveCount:    activeCount,
			AttentionCount: attentionCount,
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

	projects, _ := h.store.ListProjects(r.Context())
	labels := projectLabels(projects)

	steps, _ := h.flattenRun(r.Context(), id, 0, -1)

	// Fetch child runs for the ChildRuns field — existing flat table links
	// still use this during migration to the full tree display.
	childRuns, _ := h.store.ListChildRuns(r.Context(), id)
	var apiChildren []apiRun
	for _, c := range childRuns {
		apiChildren = append(apiChildren, toAPIRun(c, labels))
	}

	detail := apiRunDetail{
		apiRun:         toAPIRun(run, labels),
		ContainerState: h.containerState(r.Context(), run),
		Steps:          steps,
		ChildRuns:      apiChildren,
		TokenUsage:     aggregateTokenUsage(steps),
		PromptFile:     resolvePromptFile(run.ProjectDir, run.WorkflowName),
		GitRevision:    shortSHA(run.BaseSHA),
	}

	if run.State == domain.RunStateParked {
		detail.ParkedTitle = run.ParkedTitle
		detail.ParkedSeconds = h.parkedSeconds(r.Context(), id)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(detail)
}

// parkedSeconds returns how many seconds have elapsed since runID's parked
// step (the reserved "parked" result — see domain.StepParked) completed, or
// 0 if no such step is recorded. Reads raw captures directly rather than
// domain.Run.StepExecutions, which GetRun never populates (see resumeRun's
// comment on the same subtlety).
func (h *Handler) parkedSeconds(ctx context.Context, runID string) int64 {
	caps, err := h.captures.GetCaptures(ctx, runID)
	if err != nil {
		return 0
	}
	for _, s := range mergeCaptures(caps) {
		if s.Result == domain.StepParked && !s.CompletedAt.IsZero() {
			return int64(time.Since(s.CompletedAt).Seconds())
		}
	}
	return 0
}

func (h *Handler) handleAPIStepOutput(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	step := r.PathValue("step")
	logType := r.URL.Query().Get("type")  // optional: "script", "llm"
	format := r.URL.Query().Get("format") // optional: "raw" to skip parsing

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

	// Only allow stopping active runs. RunStateParked is included so a run
	// stuck awaiting a help-thread reply can be cancelled from the parked
	// pane instead of only being resumable.
	if run.State != domain.RunStatePending && run.State != domain.RunStateRunning && run.State != domain.RunStateParked {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": "run is not active"})
		return
	}

	// Prefer the daemon-level stop path (handles both host and container runs).
	if h.stopRunFn != nil && run.TaskID != "" {
		if err := h.stopRunFn(r.Context(), run.TaskID); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to stop run: %v", err)})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// Fallback: stop container directly (runs launched outside the loop).
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

// apiContainerEntry describes one retained container for the Containers view.
type apiContainerEntry struct {
	RunID        string `json:"run_id"`
	WorkflowName string `json:"workflow_name"`
	ContainerID  string `json:"container_id"`
	State        string `json:"state"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
	AgeSeconds   int64  `json:"age_seconds"`
}

// apiContainerTaskGroup groups a task's retained containers together, since a
// task can accumulate more than one kept container across attempts or child
// (host-dispatched) container runs.
type apiContainerTaskGroup struct {
	TaskID     string              `json:"task_id"`
	Title      string              `json:"title,omitempty"`
	Containers []apiContainerEntry `json:"containers"`
}

// handleAPIProjectContainers lists all retained containers for a project,
// grouped by task, with size (when the runtime supports ContainerSizer) and
// age — backing the dashboard's Containers view.
func (h *Handler) handleAPIProjectContainers(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	runs, err := h.store.ListRunsByProject(r.Context(), dir, time.Time{})
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to list runs")
		return
	}

	sizer, _ := h.container.(ports.ContainerSizer)

	var order []string
	groups := make(map[string]*apiContainerTaskGroup)
	now := time.Now()
	for _, run := range runs {
		if !run.ContainerKept || run.ContainerID == "" {
			continue
		}

		entry := apiContainerEntry{
			RunID:        run.ID,
			WorkflowName: run.WorkflowName,
			ContainerID:  run.ContainerID,
			State:        h.containerState(r.Context(), run),
		}
		age := run.StartedAt
		if !run.CompletedAt.IsZero() {
			age = run.CompletedAt
		}
		if !age.IsZero() {
			entry.AgeSeconds = int64(now.Sub(age).Seconds())
		}
		if sizer != nil {
			if size, err := sizer.ContainerSize(r.Context(), run.ContainerID); err == nil {
				entry.SizeBytes = size
			}
		}

		taskID := run.TaskID
		if taskID == "" {
			taskID = run.ID
		}
		g, seen := groups[taskID]
		if !seen {
			title := run.TaskTitle
			if title == "" {
				title = run.Title
			}
			g = &apiContainerTaskGroup{TaskID: taskID, Title: title}
			groups[taskID] = g
			order = append(order, taskID)
		}
		g.Containers = append(g.Containers, entry)
	}

	result := make([]apiContainerTaskGroup, 0, len(order))
	for _, id := range order {
		result = append(result, *groups[id])
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
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

// resolveProjectDir resolves the {name} URL segment to a project directory.
// It accepts, in order: the current URL-safe slug (see projectSlugs), the
// literal project directory (so callers that already know the absolute path
// — e.g. the CLI — can skip slug computation entirely and let the daemon map
// it), and the pre-slug "parent/base" label format for one release, so old
// bookmarks keep working. Returns (dir, slug, ok), where slug is always the
// current canonical slug for dir, suitable for building further links.
// Writes a JSON error response if not found.
func (h *Handler) resolveProjectDir(w http.ResponseWriter, r *http.Request) (string, string, bool) {
	name := r.PathValue("name")
	projects, _ := h.store.ListProjects(r.Context())
	slugs := projectSlugs(projects)
	for dir, slug := range slugs {
		if slug == name || dir == name {
			return dir, slug, true
		}
	}
	for dir, label := range legacyProjectLabels(projects) {
		if label == name {
			return dir, slugs[dir], true
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(map[string]string{"error": "project not found"})
	return "", "", false
}

// handleConsoleShell renders the console shell frame for GET /{slug} and
// GET /{slug}/{taskID}. The shell is a single-page app: project switching and
// task selection happen client-side against the JSON APIs, so this handler
// only needs to seed the initial project slug and task ID.
func (h *Handler) handleConsoleShell(w http.ResponseWriter, r *http.Request) {
	dir, slug, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	label := filepath.Base(dir)
	data := map[string]any{
		"Title":       label,
		"ProjectSlug": slug,
		"TaskID":      r.PathValue("taskID"),
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["console"].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// renderEmptyConsoleShell renders the shell with no project selected, for
// GET / when no projects are registered yet.
func (h *Handler) renderEmptyConsoleShell(w http.ResponseWriter) {
	data := map[string]any{
		"Title":       "Cloche",
		"ProjectSlug": "",
		"TaskID":      "",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.pages["console"].ExecuteTemplate(w, "layout", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// --- Legacy URL redirects ---
//
// The pre-console dashboard used /, /projects/{name}[/runs], /runs[/{id}],
// /tasks/{id}, and /failed-tasks. These routes now redirect into the new
// /{project-slug}[/{task-id}] scheme for one release before being removed
// entirely.

// handleLegacyRoot redirects GET / to the console shell for a default
// project (the alphabetically first by slug), or renders the shell with no
// project selected when none are registered yet.
func (h *Handler) handleLegacyRoot(w http.ResponseWriter, r *http.Request) {
	projects, _ := h.store.ListProjects(r.Context())
	if len(projects) == 0 {
		h.renderEmptyConsoleShell(w)
		return
	}
	slugs := projectSlugs(projects)
	sorted := make([]string, 0, len(slugs))
	for _, slug := range slugs {
		sorted = append(sorted, slug)
	}
	sort.Strings(sorted)
	http.Redirect(w, r, "/"+url.PathEscape(sorted[0]), http.StatusFound)
}

// handleLegacyProjectRedirect redirects GET /projects/{name} and
// GET /projects/{name}/runs to GET /{slug}.
func (h *Handler) handleLegacyProjectRedirect(w http.ResponseWriter, r *http.Request) {
	_, slug, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	http.Redirect(w, r, "/"+url.PathEscape(slug), http.StatusFound)
}

// handleLegacyRunsRedirect redirects GET /runs to GET /.
func (h *Handler) handleLegacyRunsRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusFound)
}

// handleLegacyRunRedirect redirects GET /runs/{id} to the console shell for
// the run's project and task, e.g. GET /{slug}/{task-id}.
func (h *Handler) handleLegacyRunRedirect(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	run, err := h.store.GetRun(r.Context(), id)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	projects, _ := h.store.ListProjects(r.Context())
	slugs := projectSlugs(projects)
	slug := slugs[run.ProjectDir]
	if slug == "" {
		slug = filepath.Base(run.ProjectDir)
	}
	dest := "/" + url.PathEscape(slug)
	if run.TaskID != "" {
		dest += "/" + url.PathEscape(run.TaskID)
	}
	http.Redirect(w, r, dest, http.StatusFound)
}

// handleLegacyTaskRedirect redirects GET /tasks/{taskID} to
// GET /{slug}/{taskID}.
func (h *Handler) handleLegacyTaskRedirect(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("taskID")
	runs, err := h.store.ListRunsFiltered(r.Context(), domain.RunListFilter{TaskID: taskID})
	if err != nil || len(runs) == 0 {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	projects, _ := h.store.ListProjects(r.Context())
	slugs := projectSlugs(projects)
	slug := slugs[runs[0].ProjectDir]
	if slug == "" {
		slug = filepath.Base(runs[0].ProjectDir)
	}
	http.Redirect(w, r, "/"+url.PathEscape(slug)+"/"+url.PathEscape(taskID), http.StatusFound)
}

// handleLegacyFailedTasksRedirect redirects GET /failed-tasks to GET /.
func (h *Handler) handleLegacyFailedTasksRedirect(w http.ResponseWriter, r *http.Request) {
	http.Redirect(w, r, "/", http.StatusFound)
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

func (h *Handler) handleAPIPromptDiff(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	file := r.URL.Query().Get("file")
	sha := r.URL.Query().Get("sha")
	if sha == "" {
		http.Error(w, "sha required", http.StatusBadRequest)
		return
	}

	// Validate sha is alphanumeric to prevent injection
	for _, c := range sha {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			http.Error(w, "invalid sha", http.StatusBadRequest)
			return
		}
	}

	// file is optional: omitted (e.g. an intent requirement's commit
	// provenance, which isn't tied to one file), the whole commit is shown.
	var cmd *exec.Cmd
	if file != "" {
		cmd = exec.Command("git", "diff", sha+"^.."+sha, "--", file)
	} else {
		cmd = exec.Command("git", "show", sha)
	}
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
		From     string `json:"from"`
		Result   string `json:"result"`
		To       string `json:"to"`
		Implicit bool   `json:"implicit,omitempty"`
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
		Builtin   bool         `json:"builtin,omitempty"`
	}

	toAPIWorkflow := func(wf *domain.Workflow, file string) apiWorkflow {
		var steps []apiStepDef
		for _, s := range wf.Steps {
			steps = append(steps, apiStepDef{
				Name:    s.Name,
				Type:    string(s.Type),
				Results: s.Results,
				Config:  s.Config,
			})
		}
		sort.Slice(steps, func(i, j int) bool { return steps[i].Name < steps[j].Name })

		var wires []apiWire
		for _, wire := range wf.Wiring {
			wires = append(wires, apiWire{From: wire.From, Result: wire.Result, To: wire.To, Implicit: wire.Implicit})
		}

		location := "container"
		if wf.Location == domain.LocationHost {
			location = "host"
		}

		return apiWorkflow{
			Name:      wf.Name,
			File:      file,
			Location:  location,
			Steps:     steps,
			Wires:     wires,
			EntryStep: wf.EntryStep,
			Builtin:   wf.Builtin,
		}
	}

	var workflows []apiWorkflow
	seen := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cloche") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(clocheDir, e.Name()))
		if err != nil {
			continue
		}
		wfs, err := dsl.ParseAll(string(data))
		if err != nil {
			continue
		}
		for _, wf := range wfs {
			seen[wf.Name] = true
			workflows = append(workflows, toAPIWorkflow(wf, filepath.Join(".cloche", e.Name())))
		}
	}

	for name, wf := range builtin.All() {
		if seen[name] {
			continue
		}
		workflows = append(workflows, toAPIWorkflow(wf, ""))
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(workflows)
}

// lookupWorkflow finds a workflow by name, scanning a project's .cloche
// files first and falling back to built-ins. Returns nil if not found.
func lookupWorkflow(dir, workflowName string) *domain.Workflow {
	clocheDir := filepath.Join(dir, ".cloche")
	entries, _ := os.ReadDir(clocheDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cloche") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(clocheDir, e.Name()))
		if err != nil {
			continue
		}
		wfs, err := dsl.ParseAll(string(data))
		if err != nil {
			continue
		}
		if found, ok := wfs[workflowName]; ok {
			return found
		}
	}
	if found, ok := builtin.Lookup(workflowName); ok {
		return found
	}
	return nil
}

// promptFileRefRegex matches the DSL's file("...") config-value syntax,
// e.g. `prompt = file(".cloche/prompts/implement.md")`.
var promptFileRefRegex = regexp.MustCompile(`^file\("(.+)"\)$`)

// resolvePromptFile returns the prompt template path configured on the
// first step (in file order) of the named workflow that references one via
// file("..."), or "" if the workflow can't be found or has no such step
// (e.g. its prompt is an inline string rather than a file reference).
func resolvePromptFile(dir, workflowName string) string {
	wf := lookupWorkflow(dir, workflowName)
	if wf == nil {
		return ""
	}
	for _, s := range wf.Steps {
		p, ok := s.Config["prompt"]
		if !ok {
			continue
		}
		if m := promptFileRefRegex.FindStringSubmatch(p); m != nil {
			return m[1]
		}
	}
	return ""
}

func (h *Handler) handleAPIStepContent(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	workflowName := r.PathValue("workflow")
	stepName := r.PathValue("step")

	wf := lookupWorkflow(dir, workflowName)
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

	// Built-in steps carry literal content (prompt text, shell script) directly
	// in their config, with no project-relative files to resolve.
	if wf.Builtin {
		if prompt := step.Config["prompt"]; prompt != "" {
			w.Write([]byte(prompt))
			return
		}
		if run := step.Config["run"]; run != "" {
			w.Write([]byte(run))
			return
		}
		http.Error(w, "no content available", http.StatusNotFound)
		return
	}

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
	if script := step.Config["poll"]; script != "" {
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

// handleAPITriggerOrchestrator triggers the orchestration loop for a project.
func (h *Handler) handleAPITriggerOrchestrator(w http.ResponseWriter, r *http.Request) {
	dir, label, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	if h.orchestrateFn == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "orchestrator not configured"})
		return
	}
	n, err := h.orchestrateFn(r.Context(), dir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "project": label, "dispatched": n})
}

// handleAPILoopStatus returns whether the orchestration loop is running for a project.
func (h *Handler) handleAPILoopStatus(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	running := h.loopStatusFn != nil && h.loopStatusFn(dir)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"running": running})
}

// handleAPILoopStop stops the orchestration loop for a project.
func (h *Handler) handleAPILoopStop(w http.ResponseWriter, r *http.Request) {
	dir, label, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	if h.stopLoopFn == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		json.NewEncoder(w).Encode(map[string]string{"error": "loop control not configured"})
		return
	}
	if err := h.stopLoopFn(r.Context(), dir); err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "project": label})
}

// handleAPILoopOccupancy returns the orchestration loop's concurrency-slot
// occupancy for a project: busy slots, queued work, and asynchronously
// driven polls. Cheap enough to poll every few seconds from a dashboard.
func (h *Handler) handleAPILoopOccupancy(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	var occupancy LoopOccupancy
	if h.occupancyProvider != nil {
		occupancy, _ = h.occupancyProvider.LoopOccupancySnapshot(dir)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(occupancy)
}

// handleAPIProjectsOccupancy returns the all-projects occupancy summary for
// a dashboard tab bar: per-project running/queued counts, health, and
// attention count.
func (h *Handler) handleAPIProjectsOccupancy(w http.ResponseWriter, r *http.Request) {
	var summary []ProjectOccupancy
	if h.occupancyProvider != nil {
		summary = h.occupancyProvider.AllLoopOccupancy()
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(summary)
}

// --- Intent API ---
//
// Backs the Project Detail page's Intent tab: requirements CRUD, the domain
// map, and scan dispatch. The intent.Store handles dormancy (a project
// without .cloche/intent/ returns empty results, never an error), so these
// handlers never need to special-case a project that hasn't scanned yet.

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// apiScope is the JSON representation of an intent.Scope.
type apiScope struct {
	Level     string   `json:"level"`
	Domains   []string `json:"domains,omitempty"`
	Paths     []string `json:"paths,omitempty"`
	Languages []string `json:"languages,omitempty"`
}

// apiProvenance is the JSON representation of an intent.Provenance, plus a
// dashboard Link resolved to the right existing page for its Kind (run
// transcripts -> Run Detail, task prompts -> Task Detail, commits -> inline
// diff, docs -> raw content), empty when the ref can't be resolved to a link.
type apiProvenance struct {
	Kind        string `json:"kind"`
	Ref         string `json:"ref"`
	ExtractedAt string `json:"extracted_at,omitempty"`
	ExtractedBy string `json:"extracted_by,omitempty"`
	Link        string `json:"link,omitempty"`
}

// apiRequirement is the JSON representation of an intent.Requirement.
type apiRequirement struct {
	ID           string        `json:"id"`
	Status       string        `json:"status"`
	SupersededBy string        `json:"superseded_by,omitempty"`
	Scope        apiScope      `json:"scope"`
	Hints        []string      `json:"hints,omitempty"`
	Confidence   string        `json:"confidence"`
	UserEdited   bool          `json:"user_edited"`
	Provenance   apiProvenance `json:"provenance"`
	Statement    string        `json:"statement"`
	Created      string        `json:"created,omitempty"`
	Updated      string        `json:"updated,omitempty"`
	NewSinceScan bool          `json:"new_since_scan,omitempty"`
}

// apiRequirementsResponse is the response body for GET .../intent/requirements.
type apiRequirementsResponse struct {
	Requirements []apiRequirement `json:"requirements"`
	LastScanAt   string           `json:"last_scan_at,omitempty"`
}

// apiDomain is the JSON representation of an intent.Domain.
type apiDomain struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Paths       []string `json:"paths"`
	UserEdited  bool     `json:"user_edited,omitempty"`
}

// apiDomainMap is the JSON representation of an intent.DomainMap.
type apiDomainMap struct {
	Version int         `json:"version"`
	Domains []apiDomain `json:"domains"`
}

// apiRequirementPatch is the request body for PATCH .../intent/requirements.
// Only present fields are applied; Statement/Scope/Hints edits set
// user_edited (matching the CLI's `intent edit`), Status changes alone (the
// table's status toggle) do not.
type apiRequirementPatch struct {
	ID        string    `json:"id"`
	Statement *string   `json:"statement"`
	Status    *string   `json:"status"`
	Scope     *apiScope `json:"scope"`
	Hints     *[]string `json:"hints"`
}

// apiTimeString formats t as RFC3339 UTC, or "" for the zero value.
func apiTimeString(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// provenanceLink resolves a Requirement's provenance to a dashboard URL, or
// "" when the kind has no linkable page (e.g. a manually added requirement).
func provenanceLink(label string, p intent.Provenance) string {
	if p.Ref == "" {
		return ""
	}
	switch p.Kind {
	case intent.ProvenanceTranscript:
		return "/runs/" + url.PathEscape(p.Ref)
	case intent.ProvenancePrompt:
		return "/tasks/" + url.PathEscape(p.Ref)
	case intent.ProvenanceCommit:
		return "/api/projects/" + url.PathEscape(label) + "/info/prompt-diff?sha=" + url.QueryEscape(p.Ref)
	case intent.ProvenanceDoc:
		return "/api/projects/" + url.PathEscape(label) + "/intent/doc?path=" + url.QueryEscape(p.Ref)
	default:
		return ""
	}
}

func toAPIScope(s intent.Scope) apiScope {
	return apiScope{Level: string(s.Level), Domains: s.Domains, Paths: s.Paths, Languages: s.Languages}
}

func toAPIRequirement(req *intent.Requirement, label string, lastScanAt time.Time) apiRequirement {
	return apiRequirement{
		ID:           req.ID,
		Status:       string(req.Status),
		SupersededBy: req.SupersededBy,
		Scope:        toAPIScope(req.Scope),
		Hints:        req.Hints,
		Confidence:   string(req.Confidence),
		UserEdited:   req.UserEdited,
		Provenance: apiProvenance{
			Kind:        string(req.Provenance.Kind),
			Ref:         req.Provenance.Ref,
			ExtractedAt: apiTimeString(req.Provenance.ExtractedAt),
			ExtractedBy: req.Provenance.ExtractedBy,
			Link:        provenanceLink(label, req.Provenance),
		},
		Statement:    req.Body,
		Created:      apiTimeString(req.Created),
		Updated:      apiTimeString(req.Updated),
		NewSinceScan: !lastScanAt.IsZero() && req.Created.After(lastScanAt),
	}
}

func toAPIDomainMap(dm *intent.DomainMap) apiDomainMap {
	out := apiDomainMap{Version: dm.Version, Domains: make([]apiDomain, len(dm.Domains))}
	for i, d := range dm.Domains {
		out.Domains[i] = apiDomain{Name: d.Name, Description: d.Description, Paths: d.Paths, UserEdited: d.UserEdited}
	}
	return out
}

// handleAPIIntentRequirementsList returns every requirement (all statuses;
// the dashboard filters superseded/disabled client-side), newest-scan-aware.
func (h *Handler) handleAPIIntentRequirementsList(w http.ResponseWriter, r *http.Request) {
	dir, label, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	store := intent.NewStore(dir)

	reqs, err := store.ListRequirements()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	scanState, err := store.LoadScanState()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := apiRequirementsResponse{
		Requirements: make([]apiRequirement, len(reqs)),
		LastScanAt:   apiTimeString(scanState.LastScanAt),
	}
	for i, req := range reqs {
		resp.Requirements[i] = toAPIRequirement(req, label, scanState.LastScanAt)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleAPIIntentRequirementsPatch updates one requirement (identified by
// patch.ID in the body) and writes it back through the file store, so the
// edit is visible in `git diff` like any other change.
func (h *Handler) handleAPIIntentRequirementsPatch(w http.ResponseWriter, r *http.Request) {
	dir, label, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	var patch apiRequirementPatch
	if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if patch.ID == "" {
		writeJSONError(w, http.StatusBadRequest, "id is required")
		return
	}

	store := intent.NewStore(dir)
	req, err := store.GetRequirement(patch.ID)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err.Error())
		return
	}

	if patch.Statement != nil {
		req.Body = *patch.Statement
		req.UserEdited = true
	}
	if patch.Scope != nil {
		req.Scope = intent.Scope{
			Level:     intent.ScopeLevel(patch.Scope.Level),
			Domains:   patch.Scope.Domains,
			Paths:     patch.Scope.Paths,
			Languages: patch.Scope.Languages,
		}
		req.UserEdited = true
	}
	if patch.Hints != nil {
		req.Hints = *patch.Hints
		req.UserEdited = true
	}
	if patch.Status != nil {
		req.Status = intent.Status(*patch.Status)
	}
	req.Updated = time.Now().UTC()

	if err := store.SaveRequirement(req); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	scanState, _ := store.LoadScanState()
	var lastScanAt time.Time
	if scanState != nil {
		lastScanAt = scanState.LastScanAt
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toAPIRequirement(req, label, lastScanAt))
}

// handleAPIIntentDomainsGet returns the project's domain map.
func (h *Handler) handleAPIIntentDomainsGet(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	dm, err := intent.NewStore(dir).LoadDomains()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toAPIDomainMap(dm))
}

// handleAPIIntentDomainsPut replaces the project's domain map wholesale (the
// domain map editor sends the full edited list back).
func (h *Handler) handleAPIIntentDomainsPut(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	var body apiDomainMap
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if body.Version == 0 {
		body.Version = 1
	}

	dm := &intent.DomainMap{Version: body.Version, Domains: make([]intent.Domain, len(body.Domains))}
	for i, d := range body.Domains {
		dm.Domains[i] = intent.Domain{Name: d.Name, Description: d.Description, Paths: d.Paths, UserEdited: d.UserEdited}
	}

	store := intent.NewStore(dir)
	if err := store.SaveDomains(dm); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(toAPIDomainMap(dm))
}

// handleAPIIntentScan dispatches an intent-scan run for the project (the
// dashboard's "Scan now" button). Requires WithScanFunc to be configured;
// the intent-scan workflow itself is defined at the project level (built-in
// or overridden), same as `changelog`/`release`.
func (h *Handler) handleAPIIntentScan(w http.ResponseWriter, r *http.Request) {
	dir, label, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	if h.scanFn == nil {
		writeJSONError(w, http.StatusNotImplemented, "intent scan not configured")
		return
	}
	runID, err := h.scanFn(r.Context(), dir)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"status": "ok", "project": label, "run_id": runID})
}

// handleAPIIntentDoc serves the raw content of a doc-provenance file within
// the project, for the requirements table's doc provenance link. path is
// project-relative; traversal outside the project root is rejected.
func (h *Handler) handleAPIIntentDoc(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}
	rel := r.URL.Query().Get("path")
	if rel == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}

	cleanDir := filepath.Clean(dir)
	full := filepath.Join(cleanDir, filepath.Clean("/"+rel))
	if full != cleanDir && !strings.HasPrefix(full, cleanDir+string(filepath.Separator)) {
		http.Error(w, "invalid path", http.StatusBadRequest)
		return
	}

	data, err := os.ReadFile(full)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
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

// builtinFailureSummary aggregates failed runs of a single built-in workflow
// (e.g. intent-scan, dispatched by the automatic post-task trigger) across all
// projects into one entry, so N automated failures show as a single
// "workflow: N failed today" line instead of masquerading as N separate open
// tasks (built-in runs are excluded from failedOpenTaskEntry grouping).
type builtinFailureSummary struct {
	WorkflowName       string `json:"workflow_name"`
	FailedCount        int    `json:"failed_count"`
	FailedToday        int    `json:"failed_today"`
	LatestRunID        string `json:"latest_run_id"`
	LatestError        string `json:"latest_error,omitempty"`
	LatestTime         string `json:"latest_time,omitempty"`
	SameErrorSignature bool   `json:"same_error_signature"`
}

// failedOpenTasksResult bundles the failed-but-open task groups with the
// separate built-in-workflow failure summary fed to the dashboard's
// attention surface as "builtin-failures".
type failedOpenTasksResult struct {
	Tasks           []failedOpenTaskEntry   `json:"tasks"`
	BuiltinFailures []builtinFailureSummary `json:"builtin_failures"`
}

// buildFailedOpenTasks returns all tasks that have failed runs but have not yet
// succeeded, plus a separate per-workflow summary of failed built-in workflow
// runs (see builtinFailureSummary). For each task the latest top-level run
// determines recency. If a taskProvider is configured, tasks that appear in
// the live bead task list are flagged as open in bead.
func (h *Handler) buildFailedOpenTasks(ctx context.Context) failedOpenTasksResult {
	runs, err := h.store.ListRuns(ctx, time.Time{})
	if err != nil {
		return failedOpenTasksResult{}
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

	// Build parent→children map and collect top-level runs, splitting off
	// built-in workflow runs into their own bucket (summarized separately
	// below rather than grouped into task entries).
	byID := map[string]*domain.Run{}
	for _, r := range runs {
		byID[r.ID] = r
	}
	parentMap := map[string][]*domain.Run{}
	var topLevel []*domain.Run
	var builtinTopLevel []*domain.Run
	for _, r := range runs {
		if r.WorkflowName == "list-tasks" {
			continue
		}
		if r.ParentRunID != "" && byID[r.ParentRunID] != nil {
			parentMap[r.ParentRunID] = append(parentMap[r.ParentRunID], r)
		} else if r.IsBuiltin {
			builtinTopLevel = append(builtinTopLevel, r)
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
	return failedOpenTasksResult{
		Tasks:           result,
		BuiltinFailures: buildBuiltinFailureSummaries(builtinTopLevel),
	}
}

// buildBuiltinFailureSummaries aggregates failed built-in workflow runs (e.g.
// intent-scan's automatic post-task trigger) into one entry per workflow
// name — the "attention model" surface for built-in failures, fed by
// buildFailedOpenTasks instead of letting repeated automated failures
// masquerade as separate open tasks.
func buildBuiltinFailureSummaries(builtinRuns []*domain.Run) []builtinFailureSummary {
	byWorkflow := map[string][]*domain.Run{}
	var order []string
	for _, r := range builtinRuns {
		if r.State != domain.RunStateFailed {
			continue
		}
		if _, ok := byWorkflow[r.WorkflowName]; !ok {
			order = append(order, r.WorkflowName)
		}
		byWorkflow[r.WorkflowName] = append(byWorkflow[r.WorkflowName], r)
	}

	todayStart := time.Now().Truncate(24 * time.Hour)

	var result []builtinFailureSummary
	for _, name := range order {
		group := byWorkflow[name]
		sort.SliceStable(group, func(i, j int) bool {
			return group[i].StartedAt.After(group[j].StartedAt)
		})
		latest := group[0]

		var failedToday int
		sameSignature := true
		for _, r := range group {
			if !r.StartedAt.Before(todayStart) {
				failedToday++
			}
			if r.ErrorMessage != latest.ErrorMessage {
				sameSignature = false
			}
		}

		result = append(result, builtinFailureSummary{
			WorkflowName:       name,
			FailedCount:        len(group),
			FailedToday:        failedToday,
			LatestRunID:        latest.ID,
			LatestError:        latest.ErrorMessage,
			LatestTime:         formatTime(latest.StartedAt),
			SameErrorSignature: sameSignature,
		})
	}
	return result
}

// handleAPIFailedTasks returns failed-but-still-open tasks together with the
// built-in workflow failure summary, as { "tasks": [...], "builtin_failures": [...] }.
func (h *Handler) handleAPIFailedTasks(w http.ResponseWriter, r *http.Request) {
	failed := h.buildFailedOpenTasks(r.Context())
	if failed.Tasks == nil {
		failed.Tasks = []failedOpenTaskEntry{}
	}
	if failed.BuiltinFailures == nil {
		failed.BuiltinFailures = []builtinFailureSummary{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(failed)
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

// projectLabels builds a mapping from full project directory paths to
// display names. The display name is always the final directory component
// (e.g. "myproject" from "/home/user/workspace/myproject"), even when two
// projects share it — the directory path itself disambiguates them in the
// UI. See projectSlugs for the unique, URL-safe identifier used in links.
func projectLabels(dirs []string) map[string]string {
	labels := make(map[string]string, len(dirs))
	for _, d := range dirs {
		labels[d] = filepath.Base(d)
	}
	return labels
}

// legacyProjectLabels reproduces the pre-slug label format ("parent/base"
// for colliding basenames, otherwise just "base"), kept only so
// resolveProjectDir can still match bookmarked URLs from before slugs
// existed. New links must use projectSlugs instead, since this format
// contains "/" and breaks when interpolated directly into an href.
func legacyProjectLabels(dirs []string) map[string]string {
	labels := make(map[string]string, len(dirs))
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

// projectSlugs builds a mapping from full project directory paths to
// unique, URL-safe slugs that never contain "/". Each slug is the final
// directory component; when two projects share a basename, the parent
// directory name is prepended, joined by "--" (e.g. "workspace--cloche" vs
// "repos--cloche"). Any further collision (e.g. two parents that already
// contain "--") is broken with a numeric suffix.
func projectSlugs(dirs []string) map[string]string {
	slugs := make(map[string]string, len(dirs))
	byBase := map[string][]string{}
	for _, d := range dirs {
		base := filepath.Base(d)
		byBase[base] = append(byBase[base], d)
	}
	for base, paths := range byBase {
		if len(paths) == 1 {
			slugs[paths[0]] = base
			continue
		}
		sort.Strings(paths)
		used := make(map[string]bool, len(paths))
		for _, p := range paths {
			parent := filepath.Base(filepath.Dir(p))
			slug := parent + "--" + base
			for n := 2; used[slug]; n++ {
				slug = fmt.Sprintf("%s--%s-%d", parent, base, n)
			}
			used[slug] = true
			slugs[p] = slug
		}
	}
	return slugs
}
