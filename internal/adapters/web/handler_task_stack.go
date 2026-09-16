package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/swordsmanluke/cloche/internal/attention"
	"github.com/swordsmanluke/cloche/internal/builtin"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/ports"
)

// Group caps for the task-stack API. Each is a bounded live/recent set;
// "earlier" done entries page past taskStackDoneCap via the cursor.
const (
	taskStackNeedsYouCap = 20
	taskStackRunningCap  = 50
	taskStackQueuedCap   = 50
	taskStackDoneCap     = 20
)

// TaskStackNeedsYou is a "needs you" entry in the task-stack response,
// mirroring attention.Item with a resolved display title.
type TaskStackNeedsYou struct {
	Kind    string   `json:"kind"`
	TaskID  string   `json:"task_id,omitempty"`
	RunID   string   `json:"run_id,omitempty"`
	Title   string   `json:"title,omitempty"`
	Reason  string   `json:"reason"`
	Since   string   `json:"since"`
	Actions []string `json:"actions,omitempty"`
	Key     string   `json:"key,omitempty"`
	// CloseAvailable reports whether the project defines a close/cancel task
	// contract (see host.ResolveCloseTaskWorkflow), so the dashboard can
	// disable the "close" action with a hint instead of only discovering
	// it's unavailable after the user clicks it.
	CloseAvailable bool `json:"close_available,omitempty"`
}

// TaskStackRunning is a task with a pending or running top-level run.
type TaskStackRunning struct {
	TaskID         string `json:"task_id"`
	Title          string `json:"title,omitempty"`
	RunID          string `json:"run_id"`
	Attempt        int    `json:"attempt"`
	CurrentStep    string `json:"current_step,omitempty"`
	StartedAt      string `json:"started_at,omitempty"`
	ElapsedSeconds int64  `json:"elapsed_seconds"`
}

// TaskStackQueued is a task or run waiting for a concurrency slot, sourced
// from the loop's occupancy model.
type TaskStackQueued struct {
	TaskID string `json:"task_id,omitempty"`
	Title  string `json:"title,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	Reason string `json:"reason"`
	Since  string `json:"since"`
}

// TaskStackDone is a task whose latest attempt reached a terminal state.
type TaskStackDone struct {
	TaskID          string `json:"task_id"`
	Title           string `json:"title,omitempty"`
	RunID           string `json:"run_id"`
	Outcome         string `json:"outcome"`
	CompletedAt     string `json:"completed_at"`
	DurationSeconds int64  `json:"duration_seconds"`
}

// TaskStack is the bounded, grouped task list the console renders, derived
// from runs in the store rather than the orchestration loop's in-memory
// snapshot (see GetLoopTasks). Cursor, when set, pages the Done group
// further into the past via the "cursor" query parameter.
type TaskStack struct {
	NeedsYou  []TaskStackNeedsYou `json:"needs_you"`
	Running   []TaskStackRunning  `json:"running"`
	Queued    []TaskStackQueued   `json:"queued"`
	DoneToday []TaskStackDone     `json:"done_today"`
	// DoneTodayDate is the daemon-local calendar date (e.g. "15 Sep") the
	// Done-today group's day boundary falls on, so the console can show the
	// cutoff in the group header rather than leaving it implicit.
	DoneTodayDate string `json:"done_today_date,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
}

// handleAPITaskStack returns the bounded, grouped task list for a project.
// Supports conditional GET via ETag/If-None-Match so a 3s poll costs only a
// round trip when nothing has changed.
func (h *Handler) handleAPITaskStack(w http.ResponseWriter, r *http.Request) {
	dir, _, ok := h.resolveProjectDir(w, r)
	if !ok {
		return
	}

	cursorParam := r.URL.Query().Get("cursor")
	cursorTime, hasCursor, err := decodeTaskStackCursor(cursorParam)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid cursor: %v", err)})
		return
	}

	stack, err := h.buildTaskStack(r.Context(), dir, cursorTime, hasCursor)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("failed to build task stack: %v", err)})
		return
	}

	body, err := json.Marshal(stack)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	etagBody, err := json.Marshal(stackForETag(stack))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	etag := taskStackETag(etagBody)
	w.Header().Set("ETag", etag)
	if match := r.Header.Get("If-None-Match"); match != "" && match == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// stackForETag returns a copy of stack with fields that change on every tick
// (independent of any real state change) zeroed out, so the ETag stays
// stable across polls when nothing but elapsed time has moved.
func stackForETag(stack *TaskStack) *TaskStack {
	clone := *stack
	if len(stack.Running) > 0 {
		clone.Running = make([]TaskStackRunning, len(stack.Running))
		for i, entry := range stack.Running {
			entry.ElapsedSeconds = 0
			clone.Running[i] = entry
		}
	}
	return &clone
}

func taskStackETag(body []byte) string {
	sum := sha256.Sum256(body)
	return `"` + hex.EncodeToString(sum[:])[:16] + `"`
}

// encodeTaskStackCursor/decodeTaskStackCursor implement an opaque cursor
// over the Done group's completion timestamp: "earlier" means "completed_at
// strictly before this cursor".
func encodeTaskStackCursor(t time.Time) string {
	return base64.RawURLEncoding.EncodeToString([]byte(t.UTC().Format(time.RFC3339Nano)))
}

func decodeTaskStackCursor(s string) (time.Time, bool, error) {
	if s == "" {
		return time.Time{}, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("decoding cursor: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}, false, fmt.Errorf("parsing cursor time: %w", err)
	}
	return t, true, nil
}

// doneTodayDateLabel formats now's calendar date in its own time zone (the
// daemon's local zone in production) as a short display label, matching the
// day boundary paginateDone uses to decide what counts as "today".
func doneTodayDateLabel(now time.Time) string {
	return now.Format("2 Jan")
}

// doneCandidate pairs a rendered Done entry with its completion time, kept
// alongside the entry so filtering/sorting doesn't need to re-parse it.
type doneCandidate struct {
	entry       TaskStackDone
	completedAt time.Time
}

// buildTaskStack derives the grouped task list for projectDir from runs (and,
// when available, tasks/attempts) in the store — never from the orchestration
// loop's in-memory snapshot. See GetLoopTasks for the snapshot this replaces.
func (h *Handler) buildTaskStack(ctx context.Context, projectDir string, cursorTime time.Time, hasCursor bool) (*TaskStack, error) {
	runs, err := h.store.ListRunsByProject(ctx, projectDir, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("listing runs: %w", err)
	}

	var tasksByID map[string]*domain.Task
	if h.taskStore != nil {
		tasks, err := h.taskStore.ListTasks(ctx, projectDir)
		if err != nil {
			return nil, fmt.Errorf("listing tasks: %w", err)
		}
		tasksByID = make(map[string]*domain.Task, len(tasks))
		for _, t := range tasks {
			tasksByID[t.ID] = t
		}
	}

	stack := &TaskStack{
		NeedsYou:  []TaskStackNeedsYou{},
		Running:   []TaskStackRunning{},
		Queued:    []TaskStackQueued{},
		DoneToday: []TaskStackDone{},
	}

	if h.attentionProvider != nil {
		items := h.attentionProvider.AttentionSnapshot(projectDir).Items
		// Resolved at most once per build (a .cloche glob+parse), and only
		// when a stale-claim/repeat-failure item actually needs it.
		closeAvailable := -1 // -1 = not yet resolved, 0 = false, 1 = true
		for _, item := range items {
			if len(stack.NeedsYou) >= taskStackNeedsYouCap {
				break
			}
			entry := TaskStackNeedsYou{
				Kind:    string(item.Kind),
				TaskID:  item.TaskID,
				RunID:   item.RunID,
				Title:   taskDisplayTitle(tasksByID[item.TaskID], nil),
				Reason:  item.Reason,
				Since:   apiTimeString(item.Since),
				Actions: item.Actions,
				Key:     item.Key,
			}
			if item.Kind == attention.KindStaleClaim || item.Kind == attention.KindRepeatFailure {
				if closeAvailable == -1 {
					_, ok := host.ResolveCloseTaskWorkflow(projectDir)
					closeAvailable = 0
					if ok {
						closeAvailable = 1
					}
				}
				entry.CloseAvailable = closeAvailable == 1
			}
			stack.NeedsYou = append(stack.NeedsYou, entry)
		}
	}

	now := time.Now()
	stack.DoneTodayDate = doneTodayDateLabel(now)
	var doneCandidates []doneCandidate

	for _, group := range groupTopLevelRunsByTask(runs) {
		taskID := group[0].TaskID
		task := tasksByID[taskID]
		if isExcludedBuiltinTask(ctx, h.taskStore, task, group) {
			continue
		}

		if active := latestRunInState(group, domain.RunStatePending, domain.RunStateRunning); active != nil {
			if len(stack.Running) < taskStackRunningCap {
				start := runStartTime(active, task)
				elapsed := time.Duration(0)
				if !start.IsZero() {
					elapsed = now.Sub(start)
				}
				stack.Running = append(stack.Running, TaskStackRunning{
					TaskID:         taskID,
					Title:          taskDisplayTitle(task, active),
					RunID:          active.ID,
					Attempt:        attemptNumber(task, active),
					CurrentStep:    strings.Join(active.ActiveSteps, ","),
					StartedAt:      apiTimeString(start),
					ElapsedSeconds: int64(elapsed.Seconds()),
				})
			}
			continue
		}

		latest := latestRunInState(group, domain.RunStateSucceeded, domain.RunStateFailed, domain.RunStateCancelled)
		if latest == nil || latest.CompletedAt.IsZero() {
			continue
		}
		doneCandidates = append(doneCandidates, doneCandidate{
			entry: TaskStackDone{
				TaskID:          taskID,
				Title:           taskDisplayTitle(task, latest),
				RunID:           latest.ID,
				Outcome:         string(latest.State),
				CompletedAt:     apiTimeString(latest.CompletedAt),
				DurationSeconds: int64(latest.CompletedAt.Sub(latest.StartedAt).Seconds()),
			},
			completedAt: latest.CompletedAt,
		})
	}

	sort.SliceStable(doneCandidates, func(i, j int) bool {
		return doneCandidates[i].completedAt.After(doneCandidates[j].completedAt)
	})
	stack.DoneToday, stack.Cursor = paginateDone(doneCandidates, cursorTime, hasCursor, now)

	if h.occupancyProvider != nil {
		if occ, ok := h.occupancyProvider.LoopOccupancySnapshot(projectDir); ok {
			for _, q := range occ.Queued {
				if len(stack.Queued) >= taskStackQueuedCap {
					break
				}
				stack.Queued = append(stack.Queued, TaskStackQueued{
					TaskID: q.TaskID,
					Title:  taskDisplayTitle(tasksByID[q.TaskID], nil),
					RunID:  q.RunID,
					Reason: q.Reason,
					Since:  q.Since,
				})
			}
		}
	}

	return stack, nil
}

// paginateDone selects the page of done candidates (already sorted newest
// first) for the current request: today's completions when no cursor is
// given, or everything strictly before the cursor otherwise. It returns the
// page and a cursor for the next "earlier" page, if more remain.
func paginateDone(candidates []doneCandidate, cursorTime time.Time, hasCursor bool, now time.Time) ([]TaskStackDone, string) {
	startOfToday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	var eligible []doneCandidate
	for _, c := range candidates {
		if hasCursor {
			if c.completedAt.Before(cursorTime) {
				eligible = append(eligible, c)
			}
		} else if !c.completedAt.Before(startOfToday) {
			eligible = append(eligible, c)
		}
	}

	page := eligible
	truncated := false
	if len(page) > taskStackDoneCap {
		page = page[:taskStackDoneCap]
		truncated = true
	}

	entries := make([]TaskStackDone, len(page))
	for i, c := range page {
		entries[i] = c.entry
	}

	switch {
	case truncated:
		return entries, encodeTaskStackCursor(page[len(page)-1].completedAt)
	case !hasCursor:
		// The (uncapped) today-view is exhausted; offer a cursor into older
		// history if any completed run precedes what was just shown.
		boundary := startOfToday
		if len(page) > 0 {
			boundary = page[len(page)-1].completedAt
		}
		for _, c := range candidates {
			if c.completedAt.Before(boundary) {
				return entries, encodeTaskStackCursor(boundary)
			}
		}
		return entries, ""
	default:
		return entries, ""
	}
}

// groupTopLevelRunsByTask groups top-level runs (not list-tasks, not a child
// of a host run) by task ID, preserving first-seen order. Runs with no task
// ID are ad-hoc `cloche run` invocations; each becomes its own single-run
// group so it renders as a synthetic single-attempt entry.
func groupTopLevelRunsByTask(runs []*domain.Run) [][]*domain.Run {
	byID := map[string]*domain.Run{}
	for _, r := range runs {
		byID[r.ID] = r
	}

	groups := map[string][]*domain.Run{}
	var order []string
	for _, r := range runs {
		if r.WorkflowName == "list-tasks" {
			continue
		}
		if r.ParentRunID != "" && byID[r.ParentRunID] != nil {
			continue
		}
		key := r.TaskID
		if key == "" {
			key = "adhoc:" + r.ID
		}
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], r)
	}

	result := make([][]*domain.Run, 0, len(order))
	for _, key := range order {
		result = append(result, groups[key])
	}
	return result
}

// latestRunInState returns the most recently started run in group whose
// State matches one of states, or nil if none match.
func latestRunInState(group []*domain.Run, states ...domain.RunState) *domain.Run {
	var latest *domain.Run
	for _, r := range group {
		matches := false
		for _, s := range states {
			if r.State == s {
				matches = true
				break
			}
		}
		if !matches {
			continue
		}
		if latest == nil || r.StartedAt.After(latest.StartedAt) {
			latest = r
		}
	}
	return latest
}

// isExcludedBuiltinTask reports whether group represents a user-initiated
// run of a built-in workflow (e.g. a manually triggered intent-scan). These
// are excluded from the task stack; the built-in grouping ticket surfaces
// them separately. Auto-triggered built-in runs (e.g. the post-task
// intent-scan) are not excluded. Fails open (not excluded) when task is nil
// (no TaskStore configured, or the task isn't in it).
func isExcludedBuiltinTask(ctx context.Context, taskStore ports.TaskStore, task *domain.Task, group []*domain.Run) bool {
	if task == nil || task.Source != domain.TaskSourceUserInitiated {
		return false
	}
	representative := latestRunInState(group, domain.RunStatePending, domain.RunStateRunning, domain.RunStateWaiting,
		domain.RunStateParked, domain.RunStateSucceeded, domain.RunStateFailed, domain.RunStateCancelled)
	if representative == nil {
		return false
	}
	if _, ok := builtin.Lookup(representative.WorkflowName); !ok {
		return false
	}
	return attention.IsUserInitiatedBuiltinRun(ctx, taskStore, representative)
}

// runStartTime resolves the best available start time for a run: the run's
// own StartedAt once it has actually started, falling back to its attempt's
// StartedAt while still pending (before Run.Start() has set StartedAt).
func runStartTime(run *domain.Run, task *domain.Task) time.Time {
	if !run.StartedAt.IsZero() {
		return run.StartedAt
	}
	if task != nil {
		for _, a := range task.Attempts {
			if a.ID == run.AttemptID {
				return a.StartedAt
			}
		}
	}
	return time.Time{}
}

// attemptNumber returns run's 1-based ordinal among task's attempts (ordered
// oldest-first by TaskStore.ListTasks), or 1 when task is nil or the attempt
// can't be found (e.g. an ad-hoc run with no Task record).
func attemptNumber(task *domain.Task, run *domain.Run) int {
	if task != nil {
		for i, a := range task.Attempts {
			if a.ID == run.AttemptID {
				return i + 1
			}
		}
	}
	return 1
}

// taskDisplayTitle prefers the Task record's title, falling back to the
// run's own TaskTitle (persisted from the tracker at dispatch time, so it
// survives the task leaving the loop's in-memory snapshot).
func taskDisplayTitle(task *domain.Task, run *domain.Run) string {
	if task != nil && task.Title != "" {
		return task.Title
	}
	if run != nil {
		return run.TaskTitle
	}
	return ""
}
