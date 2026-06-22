package grpc_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestServer_Complete_Subcommands(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	ctx := context.Background()

	// Completing the subcommand (index=1, no prefix).
	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:  []string{"cloche", ""},
		CurIdx: 1,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, "run")
	assert.Contains(t, resp.Completions, "status")
	assert.Contains(t, resp.Completions, "list")
	assert.Contains(t, resp.Completions, "logs")
}

func TestServer_Complete_SubcommandPrefix(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	ctx := context.Background()

	// Completing with "st" prefix should return "status" and "stop" but not "run".
	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:  []string{"cloche", "st"},
		CurIdx: 1,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, "status")
	assert.Contains(t, resp.Completions, "stop")
	assert.NotContains(t, resp.Completions, "run")
}

func TestServer_Complete_RunFlags(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	ctx := context.Background()

	// "cloche run <TAB>" should include --workflow.
	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:  []string{"cloche", "run", ""},
		CurIdx: 2,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, "--workflow")
}

func TestServer_Complete_WorkflowNames(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	// Create a temp project dir with a workflow file.
	dir := t.TempDir()
	clocheDir := filepath.Join(dir, ".cloche")
	os.MkdirAll(clocheDir, 0755)
	os.WriteFile(filepath.Join(clocheDir, "develop.cloche"), []byte(`workflow "develop" {
  step code {
    prompt = "do it"
    results = [success, fail]
  }
  code:success -> done
  code:fail -> abort
}
`), 0644)

	srv := server.NewClocheServer(store, nil)
	ctx := context.Background()

	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:      []string{"cloche", "run", "--workflow", ""},
		CurIdx:     3,
		ProjectDir: dir,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, "develop")
}

func TestServer_Complete_ListStateValues(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	ctx := context.Background()

	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:  []string{"cloche", "list", "--state", ""},
		CurIdx: 3,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, "running")
	assert.Contains(t, resp.Completions, "succeeded")
	assert.Contains(t, resp.Completions, "failed")
}

func TestServer_Complete_TaskIDs(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run so there is something to complete.
	run := domain.NewRun("my-task-run-abc123", "develop")
	run.ProjectDir = "/my/project"
	require.NoError(t, store.CreateRun(ctx, run))

	srv := server.NewClocheServer(store, nil)

	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:      []string{"cloche", "status", ""},
		CurIdx:     2,
		ProjectDir: "/my/project",
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, "my-task-run-abc123")
}

func TestServer_Complete_LoopSubcommands(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	ctx := context.Background()

	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:  []string{"cloche", "loop", ""},
		CurIdx: 2,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, "stop")
	assert.Contains(t, resp.Completions, "resume")
}

func TestServer_Complete_LogsTypeValues(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	ctx := context.Background()

	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:  []string{"cloche", "logs", "--type", ""},
		CurIdx: 3,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, "full")
	assert.Contains(t, resp.Completions, "script")
	assert.Contains(t, resp.Completions, "llm")
}

// --- Context-aware completion tests ---

// setupTaskWithAttempt creates a task and an attempt in the store, returning
// the task ID and attempt ID. endedAt controls when the attempt ended;
// use zero time to leave it running.
func setupTaskWithAttempt(t *testing.T, store *sqlite.Store, ctx context.Context, projectDir string, endedAt time.Time) (taskID, attemptID string) {
	t.Helper()
	task := &domain.Task{
		ID:         "task-" + domain.GenerateAttemptID(),
		Title:      "test task",
		Source:     domain.TaskSourceUserInitiated,
		ProjectDir: projectDir,
		CreatedAt:  time.Now(),
	}
	require.NoError(t, store.SaveTask(ctx, task))

	result := domain.AttemptResultRunning
	if !endedAt.IsZero() {
		result = domain.AttemptResultSucceeded
	}
	attempt := &domain.Attempt{
		ID:        domain.GenerateAttemptID(),
		TaskID:    task.ID,
		StartedAt: time.Now().Add(-5 * time.Minute),
		EndedAt:   endedAt,
		Result:    result,
	}
	require.NoError(t, store.SaveAttempt(ctx, attempt))
	return task.ID, attempt.ID
}

// TestServer_Complete_Status_OnlyActiveOrRecent verifies that "status" completions
// include running tasks and recently-completed tasks but exclude old ones.
func TestServer_Complete_Status_OnlyActiveOrRecent(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	// Running task: should appear.
	runningTaskID, _ := setupTaskWithAttempt(t, store, ctx, projectDir, time.Time{})

	// Task completed 2 minutes ago: should appear (within 10-minute window).
	recentTaskID, _ := setupTaskWithAttempt(t, store, ctx, projectDir, time.Now().Add(-2*time.Minute))

	// Task completed 30 minutes ago: should NOT appear.
	oldTaskID, _ := setupTaskWithAttempt(t, store, ctx, projectDir, time.Now().Add(-30*time.Minute))

	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)

	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:      []string{"cloche", "status", ""},
		CurIdx:     2,
		ProjectDir: projectDir,
	})
	require.NoError(t, err)

	assert.Contains(t, resp.Completions, runningTaskID, "running task should appear in status completions")
	assert.Contains(t, resp.Completions, recentTaskID, "recently-completed task should appear in status completions")
	assert.NotContains(t, resp.Completions, oldTaskID, "old task should NOT appear in status completions")
}

// TestServer_Complete_Poll_OnlyActiveOrRecent verifies that "poll" completions
// include running tasks and recently-completed tasks but exclude old ones.
func TestServer_Complete_Poll_OnlyActiveOrRecent(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	// Running task: should appear.
	runningTaskID, _ := setupTaskWithAttempt(t, store, ctx, projectDir, time.Time{})

	// Task completed 5 minutes ago: should appear.
	recentTaskID, _ := setupTaskWithAttempt(t, store, ctx, projectDir, time.Now().Add(-5*time.Minute))

	// Task completed 20 minutes ago: should NOT appear.
	oldTaskID, _ := setupTaskWithAttempt(t, store, ctx, projectDir, time.Now().Add(-20*time.Minute))

	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)

	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:      []string{"cloche", "poll", ""},
		CurIdx:     2,
		ProjectDir: projectDir,
	})
	require.NoError(t, err)

	assert.Contains(t, resp.Completions, runningTaskID, "running task should appear in poll completions")
	assert.Contains(t, resp.Completions, recentTaskID, "recently-completed task should appear in poll completions")
	assert.NotContains(t, resp.Completions, oldTaskID, "old task should NOT appear in poll completions")
}

// --- Fuzzy / component matching tests ---

// TestServer_Complete_FuzzyAttemptID verifies that typing a partial attempt ID
// expands to the full composite task:attempt ID.
func TestServer_Complete_FuzzyAttemptID(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	taskID, attemptID := setupTaskWithAttempt(t, store, ctx, projectDir, time.Time{})
	compositeID := taskID + ":" + attemptID

	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)

	// Typing the attempt ID prefix should expand to the full composite ID.
	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:      []string{"cloche", "status", attemptID},
		CurIdx:     2,
		ProjectDir: projectDir,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, compositeID, "attempt ID prefix should match composite task:attempt ID")
}

// TestServer_Complete_FuzzyAttemptIDPrefix verifies that a partial attempt ID
// (first two characters) matches the composite ID.
func TestServer_Complete_FuzzyAttemptIDPrefix(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	projectDir := t.TempDir()

	taskID, attemptID := setupTaskWithAttempt(t, store, ctx, projectDir, time.Time{})
	compositeID := taskID + ":" + attemptID

	srv := server.NewClocheServer(store, nil)
	srv.SetTaskStore(store)

	// Typing only the first two characters of the attempt ID.
	prefix := attemptID[:2]
	resp, err := srv.Complete(ctx, &pb.CompleteRequest{
		Words:      []string{"cloche", "status", prefix},
		CurIdx:     2,
		ProjectDir: projectDir,
	})
	require.NoError(t, err)
	assert.Contains(t, resp.Completions, compositeID, "partial attempt ID should match composite ID")
	// taskID is also returned if it starts with the same prefix (not guaranteed with random IDs),
	// so we only assert the composite ID is present.
	_ = taskID
}

// TestServer_Complete_FuzzyMatchesFn tests the matchesFuzzy helper via
// the filterPrefix behaviour exposed through the Complete RPC.
func TestServer_Complete_FuzzyMatchesFn(t *testing.T) {
	cases := []struct {
		s, prefix string
		want      bool
	}{
		{"task-abc:1fka", "1fka", true},  // component match
		{"task-abc:1fka", "task", true},  // prefix match on first component
		{"task-abc:1fka", "task-abc", true}, // exact component match
		{"task-abc:1fka", "xyz", false},  // no match
		{"stop", "st", true},             // normal prefix match
		{"--all", "--a", true},           // flag prefix match
		{"a:b:c", "b", true},             // middle component
		{"a:b:c", "c", true},             // last component
	}

	for _, tc := range cases {
		got := server.MatchesFuzzy(tc.s, tc.prefix)
		if got != tc.want {
			t.Errorf("matchesFuzzy(%q, %q) = %v, want %v", tc.s, tc.prefix, got, tc.want)
		}
	}
}
