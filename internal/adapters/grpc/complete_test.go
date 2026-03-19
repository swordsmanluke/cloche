package grpc_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

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
