package grpc

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
)

// A task that parks (ParkRunForHelp) must wake its DRA; only the loop's
// completeAttempt path is wired to onTaskResult, so parking never does.
func TestReview_ParkedTaskWakesDRA(t *testing.T) {
	srv, root := wakeServer(t)
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	srv.store = store
	run := domain.NewRun("run-p", "develop")
	run.ProjectDir = root
	run.TaskID = "t-1"
	run.AttemptID = "att-1"
	require.NoError(t, store.CreateRun(context.Background(), run))

	// The loop records the task's labels when it assigns the task.
	require.NoError(t, store.SetContextKey(context.Background(), "t-1", "att-1", "", host.KVTaskLabels, "auth"))

	srv.ParkRunForHelp(context.Background(), "run-p", "thread-1", "Which schema?")
	settle(t, srv)
	assert.NotEmpty(t, wakeMessages(t, root), "parked task in a DRA domain should wake the DRA")
}
