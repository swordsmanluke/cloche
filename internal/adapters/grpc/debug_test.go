package grpc_test

import (
	"strings"
	"testing"

	server "github.com/cloche-dev/cloche/internal/adapters/grpc"
	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDaemonState_NoPool(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	snap := srv.DaemonState()

	assert.Greater(t, snap.GoroutineCount, 0)
	assert.NotEmpty(t, snap.Goroutines)
	assert.Empty(t, snap.ActiveRunIDs)
	assert.Empty(t, snap.ActiveLoops)
	assert.Empty(t, snap.ContainerSessions)
}

func TestDaemonState_WithActiveRun(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	srv.AddActiveRun("run-abc", "container-123")

	snap := srv.DaemonState()

	assert.Contains(t, snap.ActiveRunIDs, "run-abc")
	assert.Empty(t, snap.ContainerSessions) // no pool configured
}

func TestDaemonState_GoroutineStackIncluded(t *testing.T) {
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	defer store.Close()

	srv := server.NewClocheServer(store, nil)
	snap := srv.DaemonState()

	// The goroutine dump from runtime.Stack always starts with "goroutine"
	assert.True(t, strings.HasPrefix(snap.Goroutines, "goroutine"),
		"goroutine dump should start with 'goroutine', got: %q", snap.Goroutines[:min(50, len(snap.Goroutines))])
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
