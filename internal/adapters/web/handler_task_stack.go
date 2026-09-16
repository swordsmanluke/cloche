package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/swordsmanluke/cloche/internal/attention"
	"github.com/swordsmanluke/cloche/internal/builtin"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/ports"
)

// Group caps for the task-stack API's live/recent groups (Needs you,
// Running, Queued — each a bounded snapshot of current state). The Done
// group instead pages: defaultDonePageSize/maxDonePageSize bound each page,
// and the "cursor" query parameter reaches further into the past.
const (
	taskStackNeedsYouCap = 20
	taskStackRunningCap  = 50
	taskStackQueuedCap   = 50

	defaultDonePageSize = 25
	maxDonePageSize     = 100

	// taskStackDoneFetchBatch is the minimum number of rows requested per
	// underlying ListDoneRunsByProject call while filling a Done page.
	// Retried tasks can shadow multiple rows down to a single deduped
	// entry, so a page's worth of query rows doesn't always yield a page's
	// worth of entries; batching bounds how many extra round trips that
	// costs without ever scanning the whole run history in one query.
	taskStackDoneFetchBatch = 50
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
// snapshot (see GetLoopTasks). Done holds every completed task ordered
// newest-first, regardless of age, one page at a time; Cursor, when set,
// pages further into the past via the "cursor" query parameter.
type TaskStack struct {
	NeedsYou []TaskStackNeedsYou `json:"needs_you"`
	Running  []TaskStackRunning  `json:"running"`
	Queued   []TaskStackQueued   `json:"queued"`
	Done     []TaskStackDone     `json:"done"`
	Cursor   string              `json:"cursor,omitempty"`
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
	cursor, hasCursor, err := decodeTaskStackCursor(cursorParam)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid cursor: %v", err)})
		return
	}

	pageSize := defaultDonePageSize
	if raw := r.URL.Query().Get("page_size"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("invalid page_size: %q", raw)})
			return
		}
		if n > maxDonePageSize {
			n = maxDonePageSize
		}
		pageSize = n
	}

	stack, err := h.buildTaskStack(r.Context(), dir, cursor, hasCursor, pageSize)
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

// doneCursor identifies a specific Done entry to page from: "earlier" means
// "everything after this entry in the (completed_at DESC, task key) order".
// Carrying the task key alongside the timestamp (rather than the timestamp
// alone) keeps pagination stable when two entries share a completed_at.
type doneCursor struct {
	CompletedAt time.Time `json:"t"`
	TaskKey     string    `json:"k"`
}

func encodeTaskStackCursor(c doneCursor) string {
	raw, _ := json.Marshal(struct {
		T string `json:"t"`
		K string `json:"k"`
	}{T: c.CompletedAt.UTC().Format(time.RFC3339Nano), K: c.TaskKey})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeTaskStackCursor(s string) (doneCursor, bool, error) {
	if s == "" {
		return doneCursor{}, false, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return doneCursor{}, false, fmt.Errorf("decoding cursor: %w", err)
	}
	var payload struct {
		T string `json:"t"`
		K string `json:"k"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return doneCursor{}, false, fmt.Errorf("parsing cursor: %w", err)
	}
	t, err := time.Parse(time.RFC3339Nano, payload.T)
	if err != nil {
		return doneCursor{}, false, fmt.Errorf("parsing cursor time: %w", err)
	}
	return doneCursor{CompletedAt: t, TaskKey: payload.K}, true, nil
}

// doneCandidate pairs a rendered Done entry with its completion time and
// task key, kept alongside the entry so cursor construction doesn't need to
// re-derive them.
type doneCandidate struct {
	entry       TaskStackDone
	completedAt time.Time
	taskKey     string
}

// buildTaskStack derives the grouped task list for projectDir from runs (and,
// when available, tasks/attempts) in the store — never from the orchestration
// loop's in-memory snapshot. See GetLoopTasks for the snapshot this replaces.
func (h *Handler) buildTaskStack(ctx context.Context, projectDir string, cursor doneCursor, hasCursor bool, pageSize int) (*TaskStack, error) {
	var attentionItems []attention.Item
	if h.attentionProvider != nil {
		attentionItems = h.attentionProvider.AttentionSnapshot(projectDir).Items
	}
	var queued []QueuedItem
	if h.occupancyProvider != nil {
		if occ, ok := h.occupancyProvider.LoopOccupancySnapshot(projectDir); ok {
			queued = occ.Queued
		}
	}

	var tasksByID map[string]*domain.Task
	if h.taskStore != nil {
		tasks, err := h.taskStore.ListTasks(ctx, projectDir)
		if err != nil {
			return nil, fmt.Errorf("listing tasks: %w", err)
		}
		tasks = filterTasksByProjectDir(tasks, projectDir)
		tasksByID = make(map[string]*domain.Task, len(tasks))
		for _, t := range tasks {
			tasksByID[t.ID] = t
		}
	}

	stack := &TaskStack{
		NeedsYou: []TaskStackNeedsYou{},
		Running:  []TaskStackRunning{},
		Queued:   []TaskStackQueued{},
		Done:     []TaskStackDone{},
	}

	// Resolved at most once per build (a .cloche glob+parse), and only when
	// a stale-claim/repeat-failure item actually needs it.
	closeAvailable := -1 // -1 = not yet resolved, 0 = false, 1 = true
	for _, item := range attentionItems {
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

	now := time.Now()
	activeTaskKeys := map[string]bool{}

	// Running: bounded to currently active runs (see domain.ActiveRunStates
	// — pending, running, waiting; inherently a small set, regardless of
	// how much completed history the project has accumulated) rather than
	// scanning every run ever recorded. A waiting run holds no concurrency
	// slot (see internal/host's occupancy model) but must still show up
	// here as a live task, not vanish — it merges into this same group with
	// a "waiting · poll <step> · ..." annotation rather than a group of its
	// own.
	var activeRuns []*domain.Run
	for _, state := range domain.ActiveRunStates {
		runs, err := h.store.ListRunsFiltered(ctx, domain.RunListFilter{ProjectDir: projectDir, State: state})
		if err != nil {
			return nil, fmt.Errorf("listing %s runs: %w", state, err)
		}
		activeRuns = append(activeRuns, filterRunsByProjectDir(runs, projectDir)...)
	}

	for _, group := range groupTopLevelRunsByTask(activeRuns) {
		taskID := group[0].TaskID
		task := tasksByID[taskID]
		if isExcludedBuiltinTask(ctx, h.taskStore, task, group) {
			continue
		}
		active := latestRunInState(group, domain.ActiveRunStates...)
		if active == nil {
			continue
		}
		activeTaskKeys[runGroupKey(active)] = true
		if len(stack.Running) < taskStackRunningCap {
			start := runStartTime(active, task)
			elapsed := time.Duration(0)
			if !start.IsZero() {
				elapsed = now.Sub(start)
			}
			currentStep := strings.Join(active.ActiveSteps, ",")
			if active.State == domain.RunStateWaiting {
				currentStep = h.waitingAnnotation(ctx, active, now)
			}
			stack.Running = append(stack.Running, TaskStackRunning{
				TaskID:         taskID,
				Title:          taskDisplayTitle(task, active),
				RunID:          active.ID,
				Attempt:        attemptNumber(task, active),
				CurrentStep:    currentStep,
				StartedAt:      apiTimeString(start),
				ElapsedSeconds: int64(elapsed.Seconds()),
			})
		}
	}

	doneEntries, nextCursor, err := h.fetchDonePage(ctx, projectDir, tasksByID, activeTaskKeys, cursor, hasCursor, pageSize)
	if err != nil {
		return nil, fmt.Errorf("listing done runs: %w", err)
	}
	stack.Done = doneEntries
	stack.Cursor = nextCursor

	for _, q := range queued {
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

	return stack, nil
}

// fetchDonePage returns one page of Done entries (newest first), starting
// strictly after cursor when hasCursor is set, deduped to at most one entry
// per task (the task's own group already picked its latest terminal run;
// here we're deduping across possibly-multiple terminal attempts of the same
// task returned by the underlying query), excluding any task that currently
// has an active run (it belongs in Running, not Done), and excluding
// user-initiated builtin-workflow runs (see isExcludedBuiltinTask) just as
// the Running group does.
//
// It queries the store in bounded batches (ListDoneRunsByProject, scoped by
// completed_at range and a row limit) rather than loading the project's
// entire run history, expanding the batch only as far as needed to fill the
// page — see the N+1 fix this preserves (cloche-eiil.19). Each batch is
// fetched inclusive of the cursor's own completed_at (ListDoneRunsByProject's
// before is an "at or before" bound), since excluding it outright would also
// silently drop any other entry sharing that exact timestamp; the loop above
// skips back past the cursor's own entry explicitly instead.
func (h *Handler) fetchDonePage(ctx context.Context, projectDir string, tasksByID map[string]*domain.Task, activeTaskKeys map[string]bool, cursor doneCursor, hasCursor bool, pageSize int) ([]TaskStackDone, string, error) {
	before := time.Time{}
	if hasCursor {
		before = cursor.CompletedAt
	}

	batchSize := pageSize + 1
	if batchSize < taskStackDoneFetchBatch {
		batchSize = taskStackDoneFetchBatch
	}

	seen := map[string]bool{}
	var page []doneCandidate
	passedCursor := !hasCursor

	for {
		batch, err := h.store.ListDoneRunsByProject(ctx, projectDir, before, batchSize)
		if err != nil {
			return nil, "", err
		}
		if len(batch) == 0 {
			return finalizeDonePage(page, pageSize, "")
		}

		for _, r := range batch {
			key := runGroupKey(r)
			if !passedCursor {
				if r.CompletedAt.Equal(cursor.CompletedAt) && key == cursor.TaskKey {
					// This is the cursor's own entry (already shown on a
					// previous page) — skip it, then start collecting from
					// whatever comes next in scan order.
					passedCursor = true
					continue
				}
				if !r.CompletedAt.Before(cursor.CompletedAt) {
					// Still within the tie cluster at the cursor's
					// timestamp but not yet at the cursor's own entry —
					// already shown on a previous page too.
					continue
				}
				// Strictly older than the cursor: the exact boundary row
				// must have been deleted since the previous page was
				// built. Treat this as the start of the next page.
				passedCursor = true
			}

			if seen[key] || activeTaskKeys[key] {
				continue
			}
			if isExcludedBuiltinTask(ctx, h.taskStore, tasksByID[r.TaskID], []*domain.Run{r}) {
				continue
			}
			seen[key] = true
			page = append(page, doneCandidate{
				entry:       buildDoneEntry(r, tasksByID[r.TaskID]),
				completedAt: r.CompletedAt,
				taskKey:     key,
			})
			if len(page) > pageSize {
				last := page[pageSize-1]
				return finalizeDonePage(page, pageSize, encodeTaskStackCursor(doneCursor{CompletedAt: last.completedAt, TaskKey: last.taskKey}))
			}
		}

		if len(batch) < batchSize {
			return finalizeDonePage(page, pageSize, "")
		}
		before = batch[len(batch)-1].CompletedAt
	}
}

func finalizeDonePage(page []doneCandidate, pageSize int, cursor string) ([]TaskStackDone, string, error) {
	if len(page) > pageSize {
		page = page[:pageSize]
	}
	entries := make([]TaskStackDone, len(page))
	for i, c := range page {
		entries[i] = c.entry
	}
	return entries, cursor, nil
}

func buildDoneEntry(r *domain.Run, task *domain.Task) TaskStackDone {
	return TaskStackDone{
		TaskID:          r.TaskID,
		Title:           taskDisplayTitle(task, r),
		RunID:           r.ID,
		Outcome:         string(r.State),
		CompletedAt:     apiTimeString(r.CompletedAt),
		DurationSeconds: int64(r.CompletedAt.Sub(r.StartedAt).Seconds()),
	}
}

// runGroupKey identifies the task-stack group a run belongs to: its task ID,
// or a synthetic per-run key for ad-hoc `cloche run` invocations (no task
// ID), so each becomes its own single-run group.
func runGroupKey(r *domain.Run) string {
	if r.TaskID != "" {
		return r.TaskID
	}
	return "adhoc:" + r.ID
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
		key := runGroupKey(r)
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

// waitingAnnotation builds the Running-group display string for a waiting
// run: "waiting · poll <step> · last poll <elapsed> ago · <n> polls" when
// poll bookkeeping (ports.PollStore) is available, degrading gracefully to
// "waiting · poll <step>" or bare "waiting" when the step name or poll
// record can't be resolved.
func (h *Handler) waitingAnnotation(ctx context.Context, run *domain.Run, now time.Time) string {
	step := ""
	if len(run.ActiveSteps) > 0 {
		step = run.ActiveSteps[0]
	}
	if step == "" {
		return "waiting"
	}
	pollStore, ok := h.store.(ports.PollStore)
	if !ok {
		return "waiting · poll " + step
	}
	rec, err := pollStore.GetPoll(ctx, run.ID, step)
	if err != nil || rec == nil {
		return "waiting · poll " + step
	}
	return fmt.Sprintf("waiting · poll %s · last poll %s ago · %d polls", step, formatDuration(rec.LastPollAt, now), rec.PollCount)
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
