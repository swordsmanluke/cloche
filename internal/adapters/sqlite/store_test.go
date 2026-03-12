package sqlite_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunStore_CreateAndGet(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-1", "test-workflow")
	run.Start()

	err = store.CreateRun(ctx, run)
	require.NoError(t, err)

	got, err := store.GetRun(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, "run-1", got.ID)
	assert.Equal(t, "test-workflow", got.WorkflowName)
	assert.Equal(t, domain.RunStateRunning, got.State)
}

func TestRunStore_Update(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("run-1", "test-workflow")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	run.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.UpdateRun(ctx, run))

	got, err := store.GetRun(ctx, "run-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, got.State)
}

func TestRunStore_List(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run1 := domain.NewRun("run-1", "wf-a")
	run2 := domain.NewRun("run-2", "wf-b")
	require.NoError(t, store.CreateRun(ctx, run1))
	require.NoError(t, store.CreateRun(ctx, run2))

	runs, err := store.ListRuns(ctx, time.Time{})
	require.NoError(t, err)
	assert.Len(t, runs, 2)
}

func TestRunStore_GetNotFound(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	_, err = store.GetRun(context.Background(), "nonexistent")
	assert.Error(t, err)
}

func TestStore_DeleteRun(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run and save a capture for it
	run := domain.NewRun("del-1", "test-workflow")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))
	require.NoError(t, store.SaveCapture(ctx, "del-1", &domain.StepExecution{
		StepName:  "step1",
		StartedAt: time.Now(),
	}))

	// Delete the run
	err = store.DeleteRun(ctx, "del-1")
	require.NoError(t, err)

	// Verify run is gone
	_, err = store.GetRun(ctx, "del-1")
	assert.Error(t, err)

	// Verify captures are gone
	caps, err := store.GetCaptures(ctx, "del-1")
	require.NoError(t, err)
	assert.Empty(t, caps)
}

func TestRunProjectDir(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("test-1", "develop")
	run.ProjectDir = "/home/user/project"
	run.Start()

	err = store.CreateRun(ctx, run)
	require.NoError(t, err)

	got, err := store.GetRun(ctx, "test-1")
	require.NoError(t, err)
	assert.Equal(t, "/home/user/project", got.ProjectDir)
}

func TestCaptureStepMetadata(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("test-1", "develop")
	run.Start()
	store.CreateRun(ctx, run)

	exec := &domain.StepExecution{
		StepName:  "implement",
		Result:    "success",
		StartedAt: time.Now(),
	}
	err = store.SaveCapture(ctx, "test-1", exec)
	require.NoError(t, err)

	caps, err := store.GetCaptures(ctx, "test-1")
	require.NoError(t, err)
	require.Len(t, caps, 1)
	assert.Equal(t, "implement", caps[0].StepName)
	assert.Equal(t, "success", caps[0].Result)
}

func TestListRunsSince(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	for i, id := range []string{"run-1", "run-2", "run-3"} {
		r := domain.NewRun(id, "develop")
		r.ProjectDir = "/project"
		r.StartedAt = time.Now().Add(time.Duration(i) * time.Minute)
		r.State = domain.RunStateRunning
		require.NoError(t, store.CreateRun(ctx, r))
	}

	runs, err := store.ListRunsSince(ctx, "/project", "develop", "run-1")
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	assert.Equal(t, "run-2", runs[0].ID)
	assert.Equal(t, "run-3", runs[1].ID)
}

func TestListRunsSinceEmpty(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	for i, id := range []string{"run-1", "run-2"} {
		r := domain.NewRun(id, "develop")
		r.ProjectDir = "/project"
		r.StartedAt = time.Now().Add(time.Duration(i) * time.Minute)
		r.State = domain.RunStateRunning
		require.NoError(t, store.CreateRun(ctx, r))
	}

	// Empty sinceRunID returns all
	runs, err := store.ListRunsSince(ctx, "/project", "develop", "")
	require.NoError(t, err)
	assert.Len(t, runs, 2)
}

func TestGetLastEvolution(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// No evolution yet
	entry, err := store.GetLastEvolution(ctx, "/project", "develop")
	require.NoError(t, err)
	assert.Nil(t, entry)

	// Save one
	require.NoError(t, store.SaveEvolution(ctx, &ports.EvolutionEntry{
		ID:           "evo-1",
		ProjectDir:   "/project",
		WorkflowName: "develop",
		TriggerRunID: "run-1",
		CreatedAt:    time.Now(),
		ChangesJSON:  "[]",
	}))

	entry, err = store.GetLastEvolution(ctx, "/project", "develop")
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "evo-1", entry.ID)
}

func TestRunTitle(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run with a title
	run := domain.NewRun("title-1", "develop")
	run.Title = "Add dark mode toggle"
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "title-1")
	require.NoError(t, err)
	assert.Equal(t, "Add dark mode toggle", got.Title)

	// Update title
	got.Title = "Updated title"
	require.NoError(t, store.UpdateRun(ctx, got))

	got2, err := store.GetRun(ctx, "title-1")
	require.NoError(t, err)
	assert.Equal(t, "Updated title", got2.Title)

	// Verify title shows up in ListRuns
	runs, err := store.ListRuns(ctx, time.Time{})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "Updated title", runs[0].Title)
}

func TestRunTitle_BackwardCompat(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run without setting Title — simulates pre-migration rows
	run := domain.NewRun("notitle-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "notitle-1")
	require.NoError(t, err)
	assert.Equal(t, "", got.Title)
}

func TestRunContainerID(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("cid-1", "develop")
	run.Start()
	run.ContainerID = "4647e7e70e3fabc123def456"

	err = store.CreateRun(ctx, run)
	require.NoError(t, err)

	got, err := store.GetRun(ctx, "cid-1")
	require.NoError(t, err)
	assert.Equal(t, "4647e7e70e3fabc123def456", got.ContainerID)

	// Test update preserves container ID
	got.ContainerID = "new-container-id"
	require.NoError(t, store.UpdateRun(ctx, got))

	got2, err := store.GetRun(ctx, "cid-1")
	require.NoError(t, err)
	assert.Equal(t, "new-container-id", got2.ContainerID)

	// Test ListRuns includes container ID
	runs, err := store.ListRuns(ctx, time.Time{})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "new-container-id", runs[0].ContainerID)
}

func TestRunContainerID_BackwardCompat(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	// Create a run without setting ContainerID — simulates pre-migration rows
	run := domain.NewRun("old-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "old-1")
	require.NoError(t, err)
	assert.Equal(t, "", got.ContainerID)
}

func TestRunErrorMessage(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("err-1", "develop")
	run.Start()
	run.Fail("failed to start container: image not found")

	err = store.CreateRun(ctx, run)
	require.NoError(t, err)

	got, err := store.GetRun(ctx, "err-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, got.State)
	assert.Equal(t, "failed to start container: image not found", got.ErrorMessage)

	// Test update preserves error message
	got.ErrorMessage = "updated error"
	require.NoError(t, store.UpdateRun(ctx, got))

	got2, err := store.GetRun(ctx, "err-1")
	require.NoError(t, err)
	assert.Equal(t, "updated error", got2.ErrorMessage)
}

func TestRunErrorMessage_Truncation(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Build a message well over 1000 characters
	longMsg := ""
	for len(longMsg) < 5000 {
		longMsg += "error output line that keeps going and going. "
	}

	// Test truncation on CreateRun
	run := domain.NewRun("trunc-create", "develop")
	run.Start()
	run.Fail(longMsg)
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "trunc-create")
	require.NoError(t, err)
	assert.LessOrEqual(t, len(got.ErrorMessage), 1020) // 1000 + "... (truncated)"
	assert.Contains(t, got.ErrorMessage, "... (truncated)")

	// Test truncation on UpdateRun
	run2 := domain.NewRun("trunc-update", "develop")
	run2.Start()
	require.NoError(t, store.CreateRun(ctx, run2))

	run2.Fail(longMsg)
	require.NoError(t, store.UpdateRun(ctx, run2))

	got2, err := store.GetRun(ctx, "trunc-update")
	require.NoError(t, err)
	assert.LessOrEqual(t, len(got2.ErrorMessage), 1020)
	assert.Contains(t, got2.ErrorMessage, "... (truncated)")

	// Short messages should NOT be truncated
	run3 := domain.NewRun("trunc-short", "develop")
	run3.Start()
	run3.Fail("short error")
	require.NoError(t, store.CreateRun(ctx, run3))

	got3, err := store.GetRun(ctx, "trunc-short")
	require.NoError(t, err)
	assert.Equal(t, "short error", got3.ErrorMessage)
}

func TestRunErrorMessageInList(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	run := domain.NewRun("err-list-1", "develop")
	run.Start()
	run.Fail("container crashed")
	require.NoError(t, store.CreateRun(ctx, run))

	runs, err := store.ListRuns(ctx, time.Time{})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.Equal(t, "container crashed", runs[0].ErrorMessage)
}

func TestStore_ConcurrentWrites(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "concurrent.db")
	store, err := sqlite.NewStore(dbPath)
	require.NoError(t, err)
	defer store.Close()

	const n = 10
	ctx := context.Background()
	errs := make([]error, n)

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("run-%d", i)
			run := domain.NewRun(id, "test-workflow")
			run.Start()
			if err := store.CreateRun(ctx, run); err != nil {
				errs[i] = err
				return
			}
			errs[i] = store.SaveCapture(ctx, id, &domain.StepExecution{
				StepName:  fmt.Sprintf("step-%d", i),
				StartedAt: time.Now(),
			})
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		assert.NoError(t, err, "goroutine %d failed", i)
	}

	runs, err := store.ListRuns(ctx, time.Time{})
	require.NoError(t, err)
	assert.Len(t, runs, n)
}

func TestListRunsByProject(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create runs for two different projects
	for i, id := range []string{"proj-a-1", "proj-a-2"} {
		r := domain.NewRun(id, "develop")
		r.ProjectDir = "/home/user/project-a"
		r.StartedAt = time.Now().Add(time.Duration(i) * time.Minute)
		r.State = domain.RunStateRunning
		require.NoError(t, store.CreateRun(ctx, r))
	}
	rB := domain.NewRun("proj-b-1", "develop")
	rB.ProjectDir = "/home/user/project-b"
	rB.StartedAt = time.Now()
	rB.State = domain.RunStateRunning
	require.NoError(t, store.CreateRun(ctx, rB))

	// Filter by project-a
	runs, err := store.ListRunsByProject(ctx, "/home/user/project-a", time.Time{})
	require.NoError(t, err)
	assert.Len(t, runs, 2)
	for _, r := range runs {
		assert.Equal(t, "/home/user/project-a", r.ProjectDir)
	}

	// Filter by project-b
	runs, err = store.ListRunsByProject(ctx, "/home/user/project-b", time.Time{})
	require.NoError(t, err)
	assert.Len(t, runs, 1)
	assert.Equal(t, "proj-b-1", runs[0].ID)

	// Filter by nonexistent project
	runs, err = store.ListRunsByProject(ctx, "/nonexistent", time.Time{})
	require.NoError(t, err)
	assert.Empty(t, runs)
}

func TestListProjects(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Empty store returns no projects
	projects, err := store.ListProjects(ctx)
	require.NoError(t, err)
	assert.Empty(t, projects)

	// Create runs for different projects
	for _, tc := range []struct {
		id, project string
	}{
		{"r1", "/home/user/alpha"},
		{"r2", "/home/user/alpha"},
		{"r3", "/home/user/beta"},
		{"r4", ""},
	} {
		r := domain.NewRun(tc.id, "develop")
		r.ProjectDir = tc.project
		r.State = domain.RunStatePending
		require.NoError(t, store.CreateRun(ctx, r))
	}

	projects, err = store.ListProjects(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"/home/user/alpha", "/home/user/beta"}, projects)
}

func TestRunContainerKept(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run with ContainerKept = false (default)
	run := domain.NewRun("kept-1", "develop")
	run.Start()
	run.ContainerID = "abc123"
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "kept-1")
	require.NoError(t, err)
	assert.False(t, got.ContainerKept)

	// Update to mark container as kept
	got.ContainerKept = true
	require.NoError(t, store.UpdateRun(ctx, got))

	got2, err := store.GetRun(ctx, "kept-1")
	require.NoError(t, err)
	assert.True(t, got2.ContainerKept)

	// Verify ListRuns includes ContainerKept
	runs, err := store.ListRuns(ctx, time.Time{})
	require.NoError(t, err)
	require.Len(t, runs, 1)
	assert.True(t, runs[0].ContainerKept)
}

func TestRunIsHost(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a host run
	run := domain.NewRun("host-1", "main")
	run.IsHost = true
	run.ProjectDir = "/project"
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "host-1")
	require.NoError(t, err)
	assert.True(t, got.IsHost)

	// Create a container run
	crun := domain.NewRun("container-1", "develop")
	crun.ProjectDir = "/project"
	crun.Start()
	require.NoError(t, store.CreateRun(ctx, crun))

	got2, err := store.GetRun(ctx, "container-1")
	require.NoError(t, err)
	assert.False(t, got2.IsHost)

	// Verify IsHost shows up in ListRuns
	runs, err := store.ListRuns(ctx, time.Time{})
	require.NoError(t, err)
	require.Len(t, runs, 2)

	hostFound := false
	containerFound := false
	for _, r := range runs {
		if r.ID == "host-1" {
			assert.True(t, r.IsHost)
			hostFound = true
		}
		if r.ID == "container-1" {
			assert.False(t, r.IsHost)
			containerFound = true
		}
	}
	assert.True(t, hostFound)
	assert.True(t, containerFound)

	// Verify IsHost shows up in ListRunsByProject
	projRuns, err := store.ListRunsByProject(ctx, "/project", time.Time{})
	require.NoError(t, err)
	require.Len(t, projRuns, 2)
	for _, r := range projRuns {
		if r.ID == "host-1" {
			assert.True(t, r.IsHost)
		}
	}

	// Test update preserves IsHost
	got.IsHost = false
	require.NoError(t, store.UpdateRun(ctx, got))
	got3, err := store.GetRun(ctx, "host-1")
	require.NoError(t, err)
	assert.False(t, got3.IsHost)
}

func TestRunIsHost_BackwardCompat(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run without setting IsHost — simulates pre-migration rows
	run := domain.NewRun("old-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "old-1")
	require.NoError(t, err)
	assert.False(t, got.IsHost)
}

func TestStore_FailStaleRuns(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create runs in different states.
	pending := domain.NewRun("pending-1", "wf")
	require.NoError(t, store.CreateRun(ctx, pending))

	running := domain.NewRun("running-1", "wf")
	running.Start()
	require.NoError(t, store.CreateRun(ctx, running))

	succeeded := domain.NewRun("succeeded-1", "wf")
	succeeded.Start()
	succeeded.Complete(domain.RunStateSucceeded)
	require.NoError(t, store.CreateRun(ctx, succeeded))

	n, err := store.FailStaleRuns(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n) // both pending and running

	got, err := store.GetRun(ctx, "pending-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, got.State)
	assert.False(t, got.CompletedAt.IsZero(), "completed_at should be set")
	assert.Equal(t, "daemon restarted while run was active", got.ErrorMessage)

	gotRunning, err := store.GetRun(ctx, "running-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateFailed, gotRunning.State)
	assert.False(t, gotRunning.CompletedAt.IsZero(), "completed_at should be set")
	assert.Equal(t, "daemon restarted while run was active", gotRunning.ErrorMessage)

	// Succeeded runs should be untouched.
	gotSucceeded, err := store.GetRun(ctx, "succeeded-1")
	require.NoError(t, err)
	assert.Equal(t, domain.RunStateSucceeded, gotSucceeded.State)
}

func TestLogFiles_SaveAndGet(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run first (foreign key)
	run := domain.NewRun("log-run-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	now := time.Now().Truncate(time.Second)

	// Save log file entries
	entries := []*ports.LogFileEntry{
		{RunID: "log-run-1", FileType: "full", FilePath: "/tmp/output/full.log", FileSize: 1024, CreatedAt: now},
		{RunID: "log-run-1", StepName: "build", FileType: "script", FilePath: "/tmp/output/build.log", FileSize: 512, CreatedAt: now},
		{RunID: "log-run-1", StepName: "build", FileType: "llm", FilePath: "/tmp/output/llm-build.log", FileSize: 2048, CreatedAt: now},
		{RunID: "log-run-1", StepName: "test", FileType: "script", FilePath: "/tmp/output/test.log", FileSize: 256, CreatedAt: now},
	}
	for _, e := range entries {
		require.NoError(t, store.SaveLogFile(ctx, e))
	}

	// GetLogFiles returns all entries for a run
	all, err := store.GetLogFiles(ctx, "log-run-1")
	require.NoError(t, err)
	assert.Len(t, all, 4)

	// GetLogFilesByStep returns entries for a specific step
	buildLogs, err := store.GetLogFilesByStep(ctx, "log-run-1", "build")
	require.NoError(t, err)
	assert.Len(t, buildLogs, 2)
	for _, lf := range buildLogs {
		assert.Equal(t, "build", lf.StepName)
	}

	// GetLogFileByType returns entries of a specific type
	llmLogs, err := store.GetLogFileByType(ctx, "log-run-1", "llm")
	require.NoError(t, err)
	assert.Len(t, llmLogs, 1)
	assert.Equal(t, "llm", llmLogs[0].FileType)
	assert.Equal(t, "build", llmLogs[0].StepName)

	// GetLogFileByType for "full"
	fullLogs, err := store.GetLogFileByType(ctx, "log-run-1", "full")
	require.NoError(t, err)
	assert.Len(t, fullLogs, 1)
	assert.Equal(t, "full", fullLogs[0].FileType)
	assert.Equal(t, int64(1024), fullLogs[0].FileSize)
}

func TestRunParentRunID(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a host (parent) run
	parent := domain.NewRun("host-run-1", "main")
	parent.IsHost = true
	parent.ProjectDir = "/project"
	parent.Start()
	require.NoError(t, store.CreateRun(ctx, parent))

	// Create child runs with ParentRunID set
	child1 := domain.NewRun("child-1", "develop")
	child1.ProjectDir = "/project"
	child1.ParentRunID = "host-run-1"
	child1.StartedAt = time.Now()
	child1.State = domain.RunStateRunning
	require.NoError(t, store.CreateRun(ctx, child1))

	child2 := domain.NewRun("child-2", "develop")
	child2.ProjectDir = "/project"
	child2.ParentRunID = "host-run-1"
	child2.StartedAt = time.Now().Add(time.Second)
	child2.State = domain.RunStateSucceeded
	require.NoError(t, store.CreateRun(ctx, child2))

	// Verify ParentRunID is persisted via GetRun
	got, err := store.GetRun(ctx, "child-1")
	require.NoError(t, err)
	assert.Equal(t, "host-run-1", got.ParentRunID)

	// Verify parent has no ParentRunID
	gotParent, err := store.GetRun(ctx, "host-run-1")
	require.NoError(t, err)
	assert.Equal(t, "", gotParent.ParentRunID)

	// ListChildRuns returns only children of the given parent
	children, err := store.ListChildRuns(ctx, "host-run-1")
	require.NoError(t, err)
	assert.Len(t, children, 2)
	assert.Equal(t, "child-1", children[0].ID)
	assert.Equal(t, "child-2", children[1].ID)

	// ListChildRuns for a non-parent returns empty
	noChildren, err := store.ListChildRuns(ctx, "child-1")
	require.NoError(t, err)
	assert.Empty(t, noChildren)

	// Verify ParentRunID shows up in ListRuns
	runs, err := store.ListRuns(ctx, time.Time{})
	require.NoError(t, err)
	for _, r := range runs {
		if r.ID == "child-1" || r.ID == "child-2" {
			assert.Equal(t, "host-run-1", r.ParentRunID)
		}
		if r.ID == "host-run-1" {
			assert.Equal(t, "", r.ParentRunID)
		}
	}

	// Test update preserves ParentRunID
	got.ParentRunID = "new-parent"
	require.NoError(t, store.UpdateRun(ctx, got))
	got2, err := store.GetRun(ctx, "child-1")
	require.NoError(t, err)
	assert.Equal(t, "new-parent", got2.ParentRunID)
}

func TestRunParentRunID_BackwardCompat(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// Create a run without setting ParentRunID — simulates pre-migration rows
	run := domain.NewRun("old-1", "develop")
	run.Start()
	require.NoError(t, store.CreateRun(ctx, run))

	got, err := store.GetRun(ctx, "old-1")
	require.NoError(t, err)
	assert.Equal(t, "", got.ParentRunID)
}

func TestLogFiles_EmptyResults(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()

	// No entries yet
	files, err := store.GetLogFiles(ctx, "nonexistent")
	require.NoError(t, err)
	assert.Empty(t, files)

	files, err = store.GetLogFilesByStep(ctx, "nonexistent", "build")
	require.NoError(t, err)
	assert.Empty(t, files)

	files, err = store.GetLogFileByType(ctx, "nonexistent", "llm")
	require.NoError(t, err)
	assert.Empty(t, files)
}

func TestListRunsSortRunningFirst(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	ctx := context.Background()
	now := time.Now()

	// Create runs: oldest running, middle succeeded, newest failed
	runs := []struct {
		id    string
		state domain.RunState
		start time.Time
	}{
		{"old-running", domain.RunStateRunning, now.Add(-10 * time.Minute)},
		{"mid-succeeded", domain.RunStateSucceeded, now.Add(-5 * time.Minute)},
		{"new-failed", domain.RunStateFailed, now.Add(-1 * time.Minute)},
		{"new-running", domain.RunStateRunning, now.Add(-2 * time.Minute)},
	}

	for _, tc := range runs {
		r := domain.NewRun(tc.id, "develop")
		r.State = tc.state
		r.StartedAt = tc.start
		r.ProjectDir = "/test/project"
		if tc.state != domain.RunStateRunning {
			r.CompletedAt = tc.start.Add(time.Minute)
		}
		require.NoError(t, store.CreateRun(ctx, r))
	}

	// ListRuns: running runs first (by recency), then non-running by recency
	listed, err := store.ListRuns(ctx, time.Time{})
	require.NoError(t, err)
	require.Len(t, listed, 4)
	assert.Equal(t, "new-running", listed[0].ID)    // running, more recent
	assert.Equal(t, "old-running", listed[1].ID)     // running, older
	assert.Equal(t, "new-failed", listed[2].ID)      // non-running, most recent
	assert.Equal(t, "mid-succeeded", listed[3].ID)   // non-running, older

	// ListRunsByProject: same ordering
	listed, err = store.ListRunsByProject(ctx, "/test/project", time.Time{})
	require.NoError(t, err)
	require.Len(t, listed, 4)
	assert.Equal(t, "new-running", listed[0].ID)
	assert.Equal(t, "old-running", listed[1].ID)
	assert.Equal(t, "new-failed", listed[2].ID)
	assert.Equal(t, "mid-succeeded", listed[3].ID)

	// ListRuns with since filter: same ordering
	listed, err = store.ListRuns(ctx, now.Add(-6*time.Minute))
	require.NoError(t, err)
	require.Len(t, listed, 3) // excludes old-running (started 10min ago)
	assert.Equal(t, "new-running", listed[0].ID)
	assert.Equal(t, "new-failed", listed[1].ID)
	assert.Equal(t, "mid-succeeded", listed[2].ID)
}
