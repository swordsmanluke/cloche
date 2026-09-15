package attention

import (
	"context"
	"testing"
	"time"

	"github.com/swordsmanluke/cloche/internal/builtin"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/ports"
)

// --- fakes (minimal RunStore/HelpStore/PollStore/TaskStore implementations
// for this package's tests only, following the codebase convention of a
// per-test-file fake rather than a shared testutil package) ---

type fakeRunStore struct {
	runs []*domain.Run
}

func (f *fakeRunStore) CreateRun(context.Context, *domain.Run) error { return nil }
func (f *fakeRunStore) GetRun(context.Context, string) (*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) GetRunByAttempt(context.Context, string, string) (*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) UpdateRun(context.Context, *domain.Run) error { return nil }
func (f *fakeRunStore) DeleteRun(context.Context, string) error      { return nil }
func (f *fakeRunStore) ListRuns(context.Context, time.Time) ([]*domain.Run, error) {
	return f.runs, nil
}
func (f *fakeRunStore) ListRunsByProject(_ context.Context, projectDir string, _ time.Time) ([]*domain.Run, error) {
	var out []*domain.Run
	for _, r := range f.runs {
		if r.ProjectDir == projectDir {
			out = append(out, r)
		}
	}
	return out, nil
}
func (f *fakeRunStore) ListRunsFiltered(context.Context, domain.RunListFilter) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) ListProjects(context.Context) ([]string, error) { return nil, nil }
func (f *fakeRunStore) ListChildRuns(context.Context, string) ([]*domain.Run, error) {
	return nil, nil
}
func (f *fakeRunStore) QueryUsage(context.Context, ports.UsageQuery) ([]domain.UsageSummary, error) {
	return nil, nil
}
func (f *fakeRunStore) GetContextKey(context.Context, string, string, string, string) (string, bool, error) {
	return "", false, nil
}
func (f *fakeRunStore) SetContextKey(context.Context, string, string, string, string, string) error {
	return nil
}
func (f *fakeRunStore) ListContextKeys(context.Context, string, string, string) ([]string, error) {
	return nil, nil
}
func (f *fakeRunStore) DeleteContextKeys(context.Context, string, string) error { return nil }
func (f *fakeRunStore) SaveAttempt(context.Context, *domain.Attempt) error      { return nil }
func (f *fakeRunStore) GetAttempt(context.Context, string) (*domain.Attempt, error) {
	return nil, nil
}
func (f *fakeRunStore) ListAttempts(context.Context, string) ([]*domain.Attempt, error) {
	return nil, nil
}
func (f *fakeRunStore) FailStaleAttempts(context.Context) (int64, error) { return 0, nil }
func (f *fakeRunStore) ListContextKVForProject(context.Context, string) ([]ports.ContextKVRow, error) {
	return nil, nil
}
func (f *fakeRunStore) AttemptTokenTotals(context.Context, string) (map[string]int64, error) {
	return nil, nil
}

type fakeHelpStore struct {
	threads map[string]*domain.HelpThread
}

func (f *fakeHelpStore) CreateThread(context.Context, *domain.HelpThread) error   { return nil }
func (f *fakeHelpStore) AppendMessage(context.Context, *domain.HelpMessage) error { return nil }
func (f *fakeHelpStore) GetThread(_ context.Context, threadID string) (*domain.HelpThread, []domain.HelpMessage, error) {
	t, ok := f.threads[threadID]
	if !ok {
		return nil, nil, nil
	}
	return t, nil, nil
}
func (f *fakeHelpStore) ResolveThread(context.Context, string) (*domain.HelpThread, error) {
	return nil, nil
}
func (f *fakeHelpStore) ListThreads(context.Context, ports.HelpThreadFilter) ([]*domain.HelpThread, error) {
	return nil, nil
}
func (f *fakeHelpStore) ListOpenThreadsByRun(context.Context, string) ([]*domain.HelpThread, error) {
	return nil, nil
}
func (f *fakeHelpStore) SetThreadState(context.Context, string, domain.ThreadState) error { return nil }
func (f *fakeHelpStore) CountThreadsWithNamePrefix(context.Context, string, string) (int, error) {
	return 0, nil
}
func (f *fakeHelpStore) ArchiveThreadsByTask(context.Context, string) (int64, error) { return 0, nil }
func (f *fakeHelpStore) DeleteArchivedThreadsOlderThan(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (f *fakeHelpStore) BindExternal(context.Context, string, string, string) error { return nil }
func (f *fakeHelpStore) ResolveExternal(context.Context, string, string) (string, error) {
	return "", nil
}
func (f *fakeHelpStore) GetExternalID(context.Context, string, string) (string, error) {
	return "", nil
}
func (f *fakeHelpStore) FindAskAnswer(context.Context, string, string) (string, string, bool, error) {
	return "", "", false, nil
}

type fakePollStore struct {
	polls map[string][]*ports.PollRecord // runID -> records
}

func (f *fakePollStore) UpsertPoll(context.Context, *ports.PollRecord) error { return nil }
func (f *fakePollStore) GetPoll(context.Context, string, string) (*ports.PollRecord, error) {
	return nil, nil
}
func (f *fakePollStore) DeletePoll(context.Context, string, string) error { return nil }
func (f *fakePollStore) ListPolls(_ context.Context, runID string) ([]*ports.PollRecord, error) {
	return f.polls[runID], nil
}

type fakeTaskStore struct {
	tasks map[string]*domain.Task
}

func (f *fakeTaskStore) SaveTask(context.Context, *domain.Task) error { return nil }
func (f *fakeTaskStore) GetTask(_ context.Context, id string) (*domain.Task, error) {
	t, ok := f.tasks[id]
	if !ok {
		return nil, nil
	}
	return t, nil
}
func (f *fakeTaskStore) ListTasks(context.Context, string) ([]*domain.Task, error) { return nil, nil }

const testProject = "/proj"

func TestCompute_Parked(t *testing.T) {
	askedAt := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	runStore := &fakeRunStore{runs: []*domain.Run{
		{
			ID:             "run-1",
			ProjectDir:     testProject,
			State:          domain.RunStateParked,
			TaskID:         "task-1",
			ParkedThreadID: "thread-1",
			ParkedTitle:    "which branch?",
		},
	}}
	helpStore := &fakeHelpStore{threads: map[string]*domain.HelpThread{
		"thread-1": {ID: "thread-1", Channel: "cloche", Name: "ask-1", UpdatedAt: askedAt},
	}}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, HelpStore: helpStore}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d: %+v", len(items), items)
	}
	got := items[0]
	if got.Kind != KindParked {
		t.Errorf("Kind = %q, want %q", got.Kind, KindParked)
	}
	if got.ThreadAddress != "cloche/ask-1" {
		t.Errorf("ThreadAddress = %q, want cloche/ask-1", got.ThreadAddress)
	}
	if !got.Since.Equal(askedAt) {
		t.Errorf("Since = %v, want %v", got.Since, askedAt)
	}
	if got.RunID != "run-1" || got.TaskID != "task-1" {
		t.Errorf("unexpected RunID/TaskID: %+v", got)
	}
}

func TestCompute_Parked_IgnoresQuiescedRuns(t *testing.T) {
	// A quiesced (operator-parked) run has State == parked but no ParkedThreadID.
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "run-1", ProjectDir: testProject, State: domain.RunStateParked},
	}}

	items, err := Compute(context.Background(), Deps{RunStore: runStore}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items for a quiesced run, got %+v", items)
	}
}

func TestCompute_StaleClaim(t *testing.T) {
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "run-1", ProjectDir: testProject, TaskID: "task-1", State: domain.RunStateFailed, StartedAt: time.Now().Add(-time.Hour), CompletedAt: time.Now().Add(-30 * time.Minute)},
	}}
	tasks := func(context.Context, string) ([]host.Task, error) {
		return []host.Task{{ID: "task-1", Status: string(host.TaskStatusInProgress)}}, nil
	}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, Tasks: tasks}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 1 || items[0].Kind != KindStaleClaim {
		t.Fatalf("expected 1 stale-claim item, got %+v", items)
	}
	if items[0].TaskID != "task-1" || items[0].RunID != "run-1" {
		t.Errorf("unexpected item: %+v", items[0])
	}
	if items[0].Key != "stale-claim:task-1" {
		t.Errorf("Key = %q, want %q", items[0].Key, "stale-claim:task-1")
	}
}

func TestCompute_StaleClaim_SuppressedByActiveRun(t *testing.T) {
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "run-1", ProjectDir: testProject, TaskID: "task-1", State: domain.RunStateRunning},
	}}
	tasks := func(context.Context, string) ([]host.Task, error) {
		return []host.Task{{ID: "task-1", Status: string(host.TaskStatusInProgress)}}, nil
	}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, Tasks: tasks}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items while a run is actively claiming the task, got %+v", items)
	}
}

func TestCompute_StaleClaim_RequiresLiveTracker(t *testing.T) {
	// No Tasks func configured (e.g. no list-tasks workflow) means we have no
	// live tracker signal, so stale-claim must fail closed.
	runStore := &fakeRunStore{runs: nil}
	items, err := Compute(context.Background(), Deps{RunStore: runStore}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items without a tracker, got %+v", items)
	}
}

func TestCompute_RepeatFailure(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "r1", ProjectDir: testProject, TaskID: "task-1", WorkflowName: "main", State: domain.RunStateFailed, StartedAt: base},
		{ID: "r2", ProjectDir: testProject, TaskID: "task-1", WorkflowName: "main", State: domain.RunStateFailed, StartedAt: base.Add(10 * time.Minute)},
		{ID: "r3", ProjectDir: testProject, TaskID: "task-1", WorkflowName: "main", State: domain.RunStateFailed, StartedAt: base.Add(20 * time.Minute)},
	}}
	tasks := func(context.Context, string) ([]host.Task, error) {
		return []host.Task{{ID: "task-1", Status: "open"}}, nil
	}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, Tasks: tasks}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 1 || items[0].Kind != KindRepeatFailure {
		t.Fatalf("expected 1 repeat-failure item, got %+v", items)
	}
	if !items[0].Since.Equal(base) {
		t.Errorf("Since = %v, want %v (oldest failure in the streak)", items[0].Since, base)
	}
	if items[0].Key != "repeat-failure:task-1" {
		t.Errorf("Key = %q, want %q", items[0].Key, "repeat-failure:task-1")
	}
}

func TestCompute_RepeatFailure_BelowThresholdOrClosed(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	makeRuns := func(taskID string) []*domain.Run {
		return []*domain.Run{
			{ID: taskID + "-r1", ProjectDir: testProject, TaskID: taskID, WorkflowName: "main", State: domain.RunStateFailed, StartedAt: base},
			{ID: taskID + "-r2", ProjectDir: testProject, TaskID: taskID, WorkflowName: "main", State: domain.RunStateFailed, StartedAt: base.Add(10 * time.Minute)},
		}
	}
	var runs []*domain.Run
	runs = append(runs, makeRuns("below-threshold")...) // only 2 failures, threshold is 3
	closedTaskRuns := []*domain.Run{
		{ID: "closed-r1", ProjectDir: testProject, TaskID: "closed", WorkflowName: "main", State: domain.RunStateFailed, StartedAt: base},
		{ID: "closed-r2", ProjectDir: testProject, TaskID: "closed", WorkflowName: "main", State: domain.RunStateFailed, StartedAt: base.Add(10 * time.Minute)},
		{ID: "closed-r3", ProjectDir: testProject, TaskID: "closed", WorkflowName: "main", State: domain.RunStateFailed, StartedAt: base.Add(20 * time.Minute)},
	}
	runs = append(runs, closedTaskRuns...)

	runStore := &fakeRunStore{runs: runs}
	tasks := func(context.Context, string) ([]host.Task, error) {
		// "closed" task doesn't appear in the tracker output at all (not open).
		return []host.Task{{ID: "below-threshold", Status: "open"}}, nil
	}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, Tasks: tasks}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items (below threshold / not open in tracker), got %+v", items)
	}
}

func TestCompute_LongPoll(t *testing.T) {
	startedAt := time.Now().Add(-3 * time.Hour)
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "run-1", ProjectDir: testProject, TaskID: "task-1", State: domain.RunStateWaiting},
	}}
	pollStore := &fakePollStore{polls: map[string][]*ports.PollRecord{
		"run-1": {{RunID: "run-1", StepName: "wait-for-ci", StartedAt: startedAt}},
	}}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, PollStore: pollStore}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 1 || items[0].Kind != KindLongPoll {
		t.Fatalf("expected 1 long-poll item, got %+v", items)
	}
	if !items[0].Since.Equal(startedAt) {
		t.Errorf("Since = %v, want %v", items[0].Since, startedAt)
	}
}

func TestCompute_LongPoll_BelowThreshold(t *testing.T) {
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "run-1", ProjectDir: testProject, State: domain.RunStateWaiting},
	}}
	pollStore := &fakePollStore{polls: map[string][]*ports.PollRecord{
		"run-1": {{RunID: "run-1", StepName: "wait-for-ci", StartedAt: time.Now().Add(-10 * time.Minute)}},
	}}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, PollStore: pollStore}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items below the poll threshold, got %+v", items)
	}
}

func TestCompute_BuiltinFailures(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "s1", ProjectDir: testProject, TaskID: "user-a1", WorkflowName: "intent-scan", IsHost: true, State: domain.RunStateFailed, StartedAt: base},
		{ID: "s2", ProjectDir: testProject, TaskID: "user-a2", WorkflowName: "intent-scan", IsHost: true, State: domain.RunStateFailed, StartedAt: base.Add(time.Minute)},
		{ID: "s3", ProjectDir: testProject, TaskID: "user-a3", WorkflowName: "intent-scan", IsHost: true, State: domain.RunStateFailed, StartedAt: base.Add(2 * time.Minute)},
	}}
	// All three runs' Tasks have empty Title (user-initiated per the CLI path).
	taskStore := &fakeTaskStore{tasks: map[string]*domain.Task{
		"user-a1": {ID: "user-a1", Title: ""},
		"user-a2": {ID: "user-a2", Title: ""},
		"user-a3": {ID: "user-a3", Title: ""},
	}}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, TaskStore: taskStore}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 1 || items[0].Kind != KindBuiltinFailures {
		t.Fatalf("expected 1 grouped builtin-failures item, got %+v", items)
	}
	if !items[0].Since.Equal(base) {
		t.Errorf("Since = %v, want %v (oldest failure)", items[0].Since, base)
	}
	if items[0].Key != "builtin-failures:intent-scan" {
		t.Errorf("Key = %q, want %q", items[0].Key, "builtin-failures:intent-scan")
	}
	found := false
	for _, a := range items[0].Actions {
		if a == "mute" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected Actions to include %q, got %v", "mute", items[0].Actions)
	}
}

func TestCompute_BuiltinFailures_ExcludesAutoTriggered(t *testing.T) {
	base := time.Now().Add(-time.Hour)
	autoTitle := builtin.AutoTriggerTitles["intent-scan"]
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "s1", ProjectDir: testProject, TaskID: "user-a1", WorkflowName: "intent-scan", IsHost: true, State: domain.RunStateFailed, StartedAt: base},
		{ID: "s2", ProjectDir: testProject, TaskID: "user-a2", WorkflowName: "intent-scan", IsHost: true, State: domain.RunStateFailed, StartedAt: base.Add(time.Minute)},
		{ID: "s3", ProjectDir: testProject, TaskID: "user-a3", WorkflowName: "intent-scan", IsHost: true, State: domain.RunStateFailed, StartedAt: base.Add(2 * time.Minute)},
	}}
	taskStore := &fakeTaskStore{tasks: map[string]*domain.Task{
		"user-a1": {ID: "user-a1", Title: autoTitle},
		"user-a2": {ID: "user-a2", Title: autoTitle},
		"user-a3": {ID: "user-a3", Title: autoTitle},
	}}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, TaskStore: taskStore}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected auto-triggered failures to be excluded, got %+v", items)
	}
}

func TestCompute_BuiltinFailures_ExcludesMuted(t *testing.T) {
	projectDir := t.TempDir()
	base := time.Now().Add(-time.Hour)
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "s1", ProjectDir: projectDir, TaskID: "user-a1", WorkflowName: "intent-scan", IsHost: true, State: domain.RunStateFailed, StartedAt: base},
		{ID: "s2", ProjectDir: projectDir, TaskID: "user-a2", WorkflowName: "intent-scan", IsHost: true, State: domain.RunStateFailed, StartedAt: base.Add(time.Minute)},
		{ID: "s3", ProjectDir: projectDir, TaskID: "user-a3", WorkflowName: "intent-scan", IsHost: true, State: domain.RunStateFailed, StartedAt: base.Add(2 * time.Minute)},
	}}
	taskStore := &fakeTaskStore{tasks: map[string]*domain.Task{
		"user-a1": {ID: "user-a1", Title: ""},
		"user-a2": {ID: "user-a2", Title: ""},
		"user-a3": {ID: "user-a3", Title: ""},
	}}

	if err := Mute(projectDir, "builtin-failures:intent-scan"); err != nil {
		t.Fatalf("Mute: %v", err)
	}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, TaskStore: taskStore}, projectDir)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected muted builtin-failures item to be excluded, got %+v", items)
	}
}

func TestCompute_Empty(t *testing.T) {
	items, err := Compute(context.Background(), Deps{RunStore: &fakeRunStore{}}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected no items, got %+v", items)
	}
}

func TestCompute_SortedBySinceAscending(t *testing.T) {
	older := time.Now().Add(-5 * time.Hour)
	newer := time.Now().Add(-1 * time.Hour)
	runStore := &fakeRunStore{runs: []*domain.Run{
		{ID: "run-1", ProjectDir: testProject, State: domain.RunStateParked, ParkedThreadID: "thread-1"},
		{ID: "run-2", ProjectDir: testProject, State: domain.RunStateWaiting},
	}}
	helpStore := &fakeHelpStore{threads: map[string]*domain.HelpThread{
		"thread-1": {ID: "thread-1", Channel: "cloche", Name: "ask-1", UpdatedAt: newer},
	}}
	pollStore := &fakePollStore{polls: map[string][]*ports.PollRecord{
		"run-2": {{RunID: "run-2", StepName: "wait", StartedAt: older}},
	}}

	items, err := Compute(context.Background(), Deps{RunStore: runStore, HelpStore: helpStore, PollStore: pollStore}, testProject)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %+v", items)
	}
	if items[0].Kind != KindLongPoll || items[1].Kind != KindParked {
		t.Errorf("expected long-poll (older) before parked (newer), got %+v", items)
	}
}
