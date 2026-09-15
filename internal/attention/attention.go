// Package attention derives the "Needs you" set: per-project items that
// require human action, gathered from state scattered across parked runs,
// the task tracker, run history, and poll records. See docs/plans for the
// design behind each kind.
package attention

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/cloche-dev/cloche/internal/builtin"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/host"
	"github.com/cloche-dev/cloche/internal/ports"
)

// Kind identifies why an Item needs attention.
type Kind string

const (
	// KindParked marks a run parked awaiting a reply on a help thread.
	KindParked Kind = "parked"
	// KindStaleClaim marks a task the tracker still shows in-progress with
	// no pending/running run claiming it.
	KindStaleClaim Kind = "stale-claim"
	// KindRepeatFailure marks a task with N or more consecutive failed
	// attempts that the tracker still shows open.
	KindRepeatFailure Kind = "repeat-failure"
	// KindLongPoll marks a poll step that has been waiting longer than the
	// configured threshold.
	KindLongPoll Kind = "long-poll"
	// KindBuiltinFailures marks repeated failures of a user-initiated
	// built-in workflow (e.g. intent-scan) within a window, grouped into a
	// single item per workflow.
	KindBuiltinFailures Kind = "builtin-failures"
)

// Item is one entry in the "Needs you" set.
type Item struct {
	Kind          Kind      `json:"kind"`
	ProjectDir    string    `json:"project_dir"`
	TaskID        string    `json:"task_id,omitempty"`
	RunID         string    `json:"run_id,omitempty"`
	Reason        string    `json:"reason"`
	Since         time.Time `json:"since"`
	Actions       []string  `json:"actions,omitempty"`
	ThreadAddress string    `json:"thread_address,omitempty"` // set for KindParked
	// Key stably identifies the item across recomputations, independent of
	// RunID (which changes as new attempts run) — used to mute a
	// KindBuiltinFailures item (see Mute) and to correlate an action taken
	// on an item across a stack refresh.
	Key string `json:"key,omitempty"`
}

// Config holds the configurable thresholds used by the derivation. Zero
// values are replaced by DefaultConfig's values in Compute.
type Config struct {
	// RepeatFailureThreshold is the number of consecutive failed attempts
	// (KindRepeatFailure) or failed runs within BuiltinFailureWindow
	// (KindBuiltinFailures) required before flagging an item.
	RepeatFailureThreshold int
	// LongPollThreshold is how long a poll step may wait before it's flagged.
	LongPollThreshold time.Duration
	// BuiltinFailureWindow bounds how far back built-in workflow runs are
	// considered for KindBuiltinFailures.
	BuiltinFailureWindow time.Duration
}

// DefaultConfig returns the package defaults.
func DefaultConfig() Config {
	return Config{
		RepeatFailureThreshold: 3,
		LongPollThreshold:      2 * time.Hour,
		BuiltinFailureWindow:   24 * time.Hour,
	}
}

// TaskLister fetches the current set of tasks from a project's task tracker
// (the list-tasks contract). Callers must fetch fresh on every call — the
// "still open in the tracker" checks below must not be answered from a
// cached loop snapshot (see host.Loop.GetTaskSnapshot).
type TaskLister func(ctx context.Context, projectDir string) ([]host.Task, error)

// Deps bundles the dependencies Compute needs. RunStore is required; the
// rest are optional and simply produce fewer kinds when absent (e.g. a
// project with no HelpStore never reports KindParked).
type Deps struct {
	RunStore  ports.RunStore
	HelpStore ports.HelpStore
	PollStore ports.PollStore
	TaskStore ports.TaskStore
	Tasks     TaskLister
	Config    Config

	// Now overrides time.Now for tests; nil uses the real clock.
	Now func() time.Time
}

// Compute derives the "Needs you" set for a single project, sorted with the
// longest-waiting item first.
func Compute(ctx context.Context, deps Deps, projectDir string) ([]Item, error) {
	if deps.RunStore == nil {
		return nil, fmt.Errorf("attention: RunStore is required")
	}

	cfg := deps.Config
	def := DefaultConfig()
	if cfg.RepeatFailureThreshold <= 0 {
		cfg.RepeatFailureThreshold = def.RepeatFailureThreshold
	}
	if cfg.LongPollThreshold <= 0 {
		cfg.LongPollThreshold = def.LongPollThreshold
	}
	if cfg.BuiltinFailureWindow <= 0 {
		cfg.BuiltinFailureWindow = def.BuiltinFailureWindow
	}
	deps.Config = cfg

	now := time.Now
	if deps.Now != nil {
		now = deps.Now
	}
	nowT := now()

	runs, err := deps.RunStore.ListRunsByProject(ctx, projectDir, time.Time{})
	if err != nil {
		return nil, fmt.Errorf("attention: listing runs: %w", err)
	}

	// The tracker must be fetched fresh here (never from a cached loop
	// snapshot) so stale-claim/repeat-failure reflect current tracker state.
	// A fetch error or an unconfigured Tasks func leaves trackerTasks nil,
	// which causes those two kinds to fail closed (produce no items) rather
	// than guess.
	var trackerTasks []host.Task
	if deps.Tasks != nil {
		if tasks, tErr := deps.Tasks(ctx, projectDir); tErr == nil {
			trackerTasks = tasks
		}
	}

	var items []Item
	items = append(items, computeParked(ctx, deps, projectDir, runs, nowT)...)
	items = append(items, computeStaleClaims(projectDir, runs, trackerTasks, nowT)...)
	items = append(items, computeRepeatFailures(projectDir, runs, trackerTasks, cfg.RepeatFailureThreshold, nowT)...)
	items = append(items, computeLongPolls(ctx, deps, projectDir, runs, nowT)...)
	items = append(items, computeBuiltinFailures(ctx, deps, projectDir, runs, nowT)...)

	sort.SliceStable(items, func(i, j int) bool { return items[i].Since.Before(items[j].Since) })
	return items, nil
}

// computeParked finds runs parked awaiting a help-thread reply. Only runs
// parked via the help-channel mechanism qualify (ParkedThreadID set); an
// operator `cloche loop quiesce` leaves it empty and is not "needs you".
func computeParked(ctx context.Context, deps Deps, projectDir string, runs []*domain.Run, now time.Time) []Item {
	var items []Item
	for _, r := range runs {
		if r.State != domain.RunStateParked || r.ParkedThreadID == "" {
			continue
		}
		since := now
		address := ""
		if deps.HelpStore != nil {
			if thread, _, err := deps.HelpStore.GetThread(ctx, r.ParkedThreadID); err == nil && thread != nil {
				address = thread.Address()
				if !thread.UpdatedAt.IsZero() {
					since = thread.UpdatedAt
				}
			}
		}
		title := r.ParkedTitle
		if title == "" {
			title = "a question"
		}
		reason := fmt.Sprintf("run parked awaiting a reply to %q", title)
		if address != "" {
			reason = fmt.Sprintf("run parked awaiting a reply on %s (%q)", address, title)
		}
		items = append(items, Item{
			Kind:          KindParked,
			ProjectDir:    projectDir,
			TaskID:        r.TaskID,
			RunID:         r.ID,
			Reason:        reason,
			Since:         since,
			Actions:       []string{"reply", "resume"},
			ThreadAddress: address,
			Key:           string(KindParked) + ":" + r.ID,
		})
	}
	return items
}

// computeStaleClaims finds tasks the tracker shows in-progress with no
// pending/running run claiming them — a claim that was never released.
func computeStaleClaims(projectDir string, runs []*domain.Run, trackerTasks []host.Task, now time.Time) []Item {
	if trackerTasks == nil {
		return nil
	}

	activeTaskIDs := map[string]bool{}
	latestByTask := map[string]*domain.Run{}
	for _, r := range runs {
		if r.TaskID == "" {
			continue
		}
		if r.State == domain.RunStatePending || r.State == domain.RunStateRunning {
			activeTaskIDs[r.TaskID] = true
		}
		if latest, ok := latestByTask[r.TaskID]; !ok || r.StartedAt.After(latest.StartedAt) {
			latestByTask[r.TaskID] = r
		}
	}

	var items []Item
	for _, t := range trackerTasks {
		if host.TaskStatus(t.Status) != host.TaskStatusInProgress || activeTaskIDs[t.ID] {
			continue
		}
		since := now
		runID := ""
		if r, ok := latestByTask[t.ID]; ok {
			runID = r.ID
			switch {
			case !r.CompletedAt.IsZero():
				since = r.CompletedAt
			case !r.StartedAt.IsZero():
				since = r.StartedAt
			}
		}
		title := t.Title
		if title == "" {
			title = t.ID
		}
		items = append(items, Item{
			Kind:       KindStaleClaim,
			ProjectDir: projectDir,
			TaskID:     t.ID,
			RunID:      runID,
			Reason:     fmt.Sprintf("tracker shows %q in progress but no run is claiming it", title),
			Since:      since,
			Actions:    []string{"release", "close", "run-once"},
			Key:        string(KindStaleClaim) + ":" + t.ID,
		})
	}
	return items
}

// computeRepeatFailures finds tasks with threshold-or-more consecutive
// failed attempts that the tracker still shows open.
func computeRepeatFailures(projectDir string, runs []*domain.Run, trackerTasks []host.Task, threshold int, now time.Time) []Item {
	if trackerTasks == nil {
		return nil
	}
	open := map[string]bool{}
	for _, t := range trackerTasks {
		if t.IsOpen() {
			open[t.ID] = true
		}
	}
	if len(open) == 0 {
		return nil
	}

	topLevel, parentMap := topLevelRuns(runs)
	grouped := map[string][]*domain.Run{}
	var order []string
	for _, r := range topLevel {
		if r.TaskID == "" || !open[r.TaskID] {
			continue
		}
		if _, seen := grouped[r.TaskID]; !seen {
			order = append(order, r.TaskID)
		}
		grouped[r.TaskID] = append(grouped[r.TaskID], r)
	}

	var items []Item
	for _, taskID := range order {
		group := grouped[taskID]
		sort.SliceStable(group, func(i, j int) bool { return group[i].StartedAt.After(group[j].StartedAt) })

		var streak []*domain.Run
		for _, r := range group {
			attemptRuns := append([]*domain.Run{r}, parentMap[r.ID]...)
			if domain.AttemptAggregateStatus(attemptRuns) != domain.RunStateFailed {
				break
			}
			streak = append(streak, r)
		}
		if len(streak) < threshold {
			continue
		}

		items = append(items, Item{
			Kind:       KindRepeatFailure,
			ProjectDir: projectDir,
			TaskID:     taskID,
			RunID:      streak[0].ID,
			Reason:     fmt.Sprintf("%d consecutive failed attempts, task still open in tracker", len(streak)),
			Since:      streak[len(streak)-1].StartedAt,
			Actions:    []string{"release", "close", "run-once"},
			Key:        string(KindRepeatFailure) + ":" + taskID,
		})
	}
	return items
}

// computeLongPolls finds poll steps that have been waiting longer than the
// configured threshold.
func computeLongPolls(ctx context.Context, deps Deps, projectDir string, runs []*domain.Run, now time.Time) []Item {
	if deps.PollStore == nil {
		return nil
	}

	var items []Item
	for _, r := range runs {
		if r.State != domain.RunStateWaiting {
			continue
		}
		polls, err := deps.PollStore.ListPolls(ctx, r.ID)
		if err != nil {
			continue
		}
		for _, p := range polls {
			if p.StartedAt.IsZero() {
				continue
			}
			elapsed := now.Sub(p.StartedAt)
			if elapsed < deps.Config.LongPollThreshold {
				continue
			}
			items = append(items, Item{
				Kind:       KindLongPoll,
				ProjectDir: projectDir,
				TaskID:     r.TaskID,
				RunID:      r.ID,
				Reason:     fmt.Sprintf("poll step %q waiting %s (threshold %s)", p.StepName, elapsed.Round(time.Second), deps.Config.LongPollThreshold),
				Since:      p.StartedAt,
				Actions:    []string{"logs", "cancel"},
			})
		}
	}
	return items
}

// computeBuiltinFailures groups repeated failures of a user-initiated
// built-in workflow (within Config.BuiltinFailureWindow) into a single item
// per workflow name.
func computeBuiltinFailures(ctx context.Context, deps Deps, projectDir string, runs []*domain.Run, now time.Time) []Item {
	threshold := deps.Config.RepeatFailureThreshold
	windowStart := now.Add(-deps.Config.BuiltinFailureWindow)

	byWorkflow := map[string][]*domain.Run{}
	var order []string
	for _, r := range runs {
		if !r.IsHost || r.StartedAt.Before(windowStart) {
			continue
		}
		if _, ok := builtin.Lookup(r.WorkflowName); !ok {
			continue
		}
		if !IsUserInitiatedBuiltinRun(ctx, deps.TaskStore, r) {
			continue
		}
		if _, seen := byWorkflow[r.WorkflowName]; !seen {
			order = append(order, r.WorkflowName)
		}
		byWorkflow[r.WorkflowName] = append(byWorkflow[r.WorkflowName], r)
	}

	var items []Item
	for _, name := range order {
		var failed []*domain.Run
		for _, r := range byWorkflow[name] {
			if r.State == domain.RunStateFailed {
				failed = append(failed, r)
			}
		}
		if len(failed) < threshold {
			continue
		}
		sort.SliceStable(failed, func(i, j int) bool { return failed[i].StartedAt.Before(failed[j].StartedAt) })
		oldest := failed[0]
		latest := failed[len(failed)-1]

		key := string(KindBuiltinFailures) + ":" + name
		if IsMuted(projectDir, key) {
			continue
		}

		items = append(items, Item{
			Kind:       KindBuiltinFailures,
			ProjectDir: projectDir,
			RunID:      latest.ID,
			Reason:     fmt.Sprintf("%d failed %q runs in the last %s", len(failed), name, deps.Config.BuiltinFailureWindow),
			Since:      oldest.StartedAt,
			Actions:    []string{"retry", "logs", "mute"},
			Key:        key,
		})
	}
	return items
}

// IsUserInitiatedBuiltinRun reports whether run r of a built-in workflow was
// started by the user rather than an automatic daemon trigger (e.g. the
// post-task intent-scan trigger). See builtin.AutoTriggerTitles for why this
// requires a Task lookup rather than a field on Run. Fails open (true) when
// it can't be determined, since builtin.AutoTriggerTitles has no entry (no
// known auto-trigger to rule out) or taskStore/TaskID is unavailable.
//
// Exported for reuse by the task-stack API (internal/adapters/web), which
// excludes user-initiated built-in runs from its groups so they can be
// surfaced separately by the built-in grouping ticket.
func IsUserInitiatedBuiltinRun(ctx context.Context, taskStore ports.TaskStore, r *domain.Run) bool {
	autoTitle, hasAuto := builtin.AutoTriggerTitles[r.WorkflowName]
	if !hasAuto {
		return true
	}
	if taskStore == nil || r.TaskID == "" {
		return true
	}
	task, err := taskStore.GetTask(ctx, r.TaskID)
	if err != nil || task == nil {
		return true
	}
	return task.Title != autoTitle
}

// topLevelRuns splits runs into top-level (non-child, non-list-tasks) runs
// and a parent-run-ID -> child-runs map, mirroring the grouping used by the
// failed-open-tasks dashboard (internal/adapters/web.buildFailedOpenTasks).
func topLevelRuns(runs []*domain.Run) ([]*domain.Run, map[string][]*domain.Run) {
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
	return topLevel, parentMap
}
