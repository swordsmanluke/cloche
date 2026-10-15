package host

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/domain"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// kvStore is a fakeStore whose GetContextKey falls back from the run scope
// to the attempt scope, like the real store.
type kvStore struct{ *fakeStore }

func (s kvStore) GetContextKey(ctx context.Context, taskID, attemptID, runID, key string) (string, bool, error) {
	if v, ok, err := s.fakeStore.GetContextKey(ctx, taskID, attemptID, runID, key); ok || err != nil {
		return v, ok, err
	}
	return s.fakeStore.GetContextKey(ctx, taskID, attemptID, "", key)
}

func newSQLiteStore(t *testing.T) kvStore {
	t.Helper()
	return kvStore{&fakeStore{runs: map[string]*domain.Run{}}}
}

const claimWF = `workflow claim-task {
  host {}
  contract {
    kv:  task_id
    out: task_title
    out: task_body_path
  }
  step claim {
    run = "%s"
    results = [success, fail]
  }
  claim:success -> done
  claim:fail -> abort
}
`

func claimProject(t *testing.T, script string) string {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".cloche", "host.cloche"), fmt.Sprintf(claimWF, script))
	return dir
}

func TestRunClaimTask_NoPhaseResolves(t *testing.T) {
	dir := t.TempDir()
	res, err := RunClaimTask(context.Background(), &Runner{Store: newSQLiteStore(t)}, dir, "t1", "a1")
	require.NoError(t, err)
	assert.Nil(t, res)
}

func TestRunClaimTask_PromotesOutputs(t *testing.T) {
	dir := claimProject(t, "true")
	store := newSQLiteStore(t)
	ctx := context.Background()
	// Stand-in for the workflow's `clo set` calls (attempt scope is visible to the run).
	require.NoError(t, store.SetContextKey(ctx, "t1", "a1", "", "task_title", "Fix it"))
	require.NoError(t, store.SetContextKey(ctx, "t1", "a1", "", "task_body_path", "body.md"))

	res, err := RunClaimTask(ctx, &Runner{Store: store}, dir, "t1", "a1")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.False(t, res.Unavailable)
	assert.Equal(t, "Fix it", res.Title)
	assert.Equal(t, "body.md", res.BodyPath)
	v, ok, _ := store.GetContextKey(ctx, "t1", "a1", "", "task_id")
	assert.True(t, ok)
	assert.Equal(t, "t1", v)
}

func TestRunClaimTask_MissingOutputIsContractError(t *testing.T) {
	dir := claimProject(t, "true")
	_, err := RunClaimTask(context.Background(), &Runner{Store: newSQLiteStore(t)}, dir, "t1", "a1")
	assert.ErrorContains(t, err, "task_title")
}

func TestRunClaimTask_UnavailableIsNotAnError(t *testing.T) {
	dir := claimProject(t, "false")
	store := newSQLiteStore(t)
	ctx := context.Background()
	require.NoError(t, store.SetContextKey(ctx, "t1", "a1", "", "claim_status", "unavailable"))
	res, err := RunClaimTask(ctx, &Runner{Store: store}, dir, "t1", "a1")
	require.NoError(t, err)
	require.NotNil(t, res)
	assert.True(t, res.Unavailable)
}

func TestRunClaimTask_PlainFailureIsError(t *testing.T) {
	dir := claimProject(t, "false")
	res, err := RunClaimTask(context.Background(), &Runner{Store: newSQLiteStore(t)}, dir, "t1", "a1")
	assert.Error(t, err)
	assert.Nil(t, res)
}

func TestResolvePhase_RootBindToModule(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, ".cloche", "modules", "tracker")
	writeFile(t, filepath.Join(mod, "module.toml"), "name = \"tracker\"\ndescription = \"t\"\nexports = [\"close-task\"]\n")
	writeFile(t, filepath.Join(mod, "tasks.cloche"), `workflow close-task {
  host {}
  step c { run = "true" results = [success, fail] }
  c:success -> done
  c:fail -> abort
}
`)
	_, ok := ResolveCloseTaskWorkflow(dir)
	assert.False(t, ok)

	writeFile(t, filepath.Join(dir, ".cloche", "project.cloche"), "bind close-task = tracker\n")
	name, ok := ResolveCloseTaskWorkflow(dir)
	assert.True(t, ok)
	assert.Equal(t, "close-task", name)

	wf, err := findHostWorkflow(dir, "close-task")
	require.NoError(t, err)
	assert.Equal(t, "tracker", wf.Module)
}

func TestRunListTasksWorkflow_TasksPathPreferredOverStdout(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "tasks.jsonl"), `{"id":"from-file","status":"open","title":"F"}`+"\n")
	writeFile(t, filepath.Join(dir, ".cloche", "host.cloche"), `workflow list-tasks {
  host {}
  step list { run = "echo '{\"id\":\"from-stdout\"}'" results = [success, fail] }
  list:success -> done
  list:fail -> abort
}
`)
	store := newSQLiteStore(t)
	// tasks_path is written by the workflow via `clo set`; emulate with a
	// store wrapper that answers for the synthetic namespace.
	runner := &Runner{Store: &tasksPathStore{kvStore: store, path: "tasks.jsonl"}}
	tasks, _, err := RunListTasksWorkflow(context.Background(), runner, dir)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "from-file", tasks[0].ID)
}

func TestRunListTasksWorkflow_StdoutFallback(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".cloche", "host.cloche"), `workflow list-tasks {
  host {}
  step list { run = "echo '{\"id\":\"from-stdout\"}'" results = [success, fail] }
  list:success -> done
  list:fail -> abort
}
`)
	tasks, _, err := RunListTasksWorkflow(context.Background(), &Runner{Store: newSQLiteStore(t)}, dir)
	require.NoError(t, err)
	require.Len(t, tasks, 1)
	assert.Equal(t, "from-stdout", tasks[0].ID)
}

// tasksPathStore reports a tasks_path value for the list-tasks namespace.
type tasksPathStore struct {
	kvStore
	path string
}

func (s *tasksPathStore) GetContextKey(ctx context.Context, taskID, attemptID, runID, key string) (string, bool, error) {
	if key == "tasks_path" && taskID == "list-tasks" {
		return s.path, true, nil
	}
	return s.kvStore.GetContextKey(ctx, taskID, attemptID, runID, key)
}

func TestPhaseLoop_ClaimPhase(t *testing.T) {
	cases := []struct {
		name      string
		claim     ClaimFunc
		wantMain  bool
		wantTitle string
	}{
		{"claimed title overrides", func(context.Context, string, string, string) (*ClaimResult, error) {
			return &ClaimResult{Title: "Claimed"}, nil
		}, true, "Claimed"},
		{"no claim phase result runs main", func(context.Context, string, string, string) (*ClaimResult, error) {
			return nil, nil
		}, true, "Listed"},
		{"unavailable skips main", func(context.Context, string, string, string) (*ClaimResult, error) {
			return &ClaimResult{Unavailable: true}, nil
		}, false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &fakeStore{runs: map[string]*domain.Run{}}
			var mainCalls atomic.Int32
			var mu sync.Mutex
			var title string
			loop := NewPhaseLoop(LoopConfig{ProjectDir: "/tmp/p", MaxConcurrent: 1, DedupTimeout: time.Minute}, store,
				func(context.Context, string) ([]Task, error) {
					return []Task{{ID: "t1", Status: "open", Title: "Listed"}}, nil
				},
				func(_ context.Context, _ string, _ string, ttl string, _ string) (*RunResult, error) {
					mainCalls.Add(1)
					mu.Lock()
					title = ttl
					mu.Unlock()
					return &RunResult{State: domain.RunStateSucceeded}, nil
				})
			loop.SetClaimFunc(tc.claim)
			loop.Start()
			time.Sleep(300 * time.Millisecond)
			loop.Stop()
			mu.Lock()
			defer mu.Unlock()
			if tc.wantMain {
				assert.GreaterOrEqual(t, int(mainCalls.Load()), 1)
				assert.Equal(t, tc.wantTitle, title)
			} else {
				assert.Zero(t, mainCalls.Load())
			}
		})
	}
}
