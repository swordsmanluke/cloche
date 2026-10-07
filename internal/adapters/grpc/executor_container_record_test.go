package grpc

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/docker"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/ports"
)

// rowRunStore is fakeRunStore with one real row, so tests can observe what
// the executor writes back to the host run.
type rowRunStore struct {
	fakeRunStore
	row     *domain.Run
	updates int
}

func (s *rowRunStore) GetRun(_ context.Context, id string) (*domain.Run, error) {
	if s.row == nil || s.row.ID != id {
		return nil, assert.AnError
	}
	cp := *s.row
	return &cp, nil
}

func (s *rowRunStore) UpdateRun(_ context.Context, r *domain.Run) error {
	cp := *r
	s.row = &cp
	s.updates++
	return nil
}

func newHostRunExecutor(t *testing.T, pool *docker.ContainerPool, store *rowRunStore) *DaemonExecutor {
	t.Helper()
	de := NewDaemonExecutor(DaemonExecutorConfig{
		Pool:       pool,
		Store:      store,
		ProjectDir: t.TempDir(),
		TaskID:     "task-1",
		AttemptID:  "att-1",
	})
	de.SetHostExecutor(&host.Executor{HostRunID: store.row.ID})
	return de
}

// establishSession drives the pool through Start + AgentReady for poolKey
// and returns the session's container ID.
func establishSession(t *testing.T, pool *docker.ContainerPool, poolKey string) string {
	t.Helper()
	go func() {
		time.Sleep(10 * time.Millisecond)
		pool.NotifyReady("fake-container-id") // recordingContainerRuntime.Start's fixed ID
	}()
	sess, err := pool.SessionFor(context.Background(), poolKey, ports.ContainerConfig{Image: "img"})
	require.NoError(t, err)
	return sess.ContainerID
}

// TestDaemonExecutor_RecordsSessionContainerOnHostRun verifies that the
// container backing a sub-workflow session is persisted on the host run row
// (the console's run detail reads container_id/container_state from the
// row, and showed "(removed)" for every live host run while it was empty),
// and that reusing the same session doesn't rewrite the row.
func TestDaemonExecutor_RecordsSessionContainerOnHostRun(t *testing.T) {
	store := &rowRunStore{row: &domain.Run{ID: "att-1-main", IsHost: true, State: domain.RunStateRunning}}
	de := newHostRunExecutor(t, docker.NewContainerPool(&recordingContainerRuntime{}), store)

	de.recordSessionContainer(context.Background(), "att-1:default", "cid-1")
	assert.Equal(t, "cid-1", store.row.ContainerID)
	assert.Equal(t, 1, store.updates)

	de.recordSessionContainer(context.Background(), "att-1:default", "cid-1")
	assert.Equal(t, 1, store.updates, "unchanged container ID must not rewrite the row")

	assert.False(t, store.row.ContainerKept)
}

// TestDaemonExecutor_Close_FlagsKeptContainerOnFailure verifies that when the
// host workflow fails, the container the pool stops-but-keeps for debugging
// is flagged ContainerKept on the host run — otherwise the containers
// dashboard (which lists ContainerKept rows only) can neither show nor prune
// it. On success the container is removed and the flag stays clear.
func TestDaemonExecutor_Close_FlagsKeptContainerOnFailure(t *testing.T) {
	for _, succeeded := range []bool{false, true} {
		rt := &recordingContainerRuntime{}
		pool := docker.NewContainerPool(rt)
		store := &rowRunStore{row: &domain.Run{ID: "att-1-main", IsHost: true, State: domain.RunStateRunning}}
		de := newHostRunExecutor(t, pool, store)

		poolKey := "att-1:default"
		cid := establishSession(t, pool, poolKey)
		de.poolKeys = map[string]bool{poolKey: true}
		de.recordSessionContainer(context.Background(), poolKey, cid)

		de.Close(succeeded)

		assert.Equal(t, cid, store.row.ContainerID, "succeeded=%v", succeeded)
		assert.Equal(t, !succeeded, store.row.ContainerKept, "succeeded=%v", succeeded)
	}
}

// TestDaemonExecutor_Close_ParkedContainerIsNotKept verifies that a session
// already torn down by the parked path (container committed to an image and
// removed) is not reported as kept when the host run later closes unsuccessfully.
func TestDaemonExecutor_Close_ParkedContainerIsNotKept(t *testing.T) {
	rt := &recordingContainerRuntime{}
	pool := docker.NewContainerPool(rt)
	store := &rowRunStore{row: &domain.Run{ID: "att-1-main", IsHost: true, State: domain.RunStateRunning}}
	de := newHostRunExecutor(t, pool, store)

	poolKey := "att-1:default"
	cid := establishSession(t, pool, poolKey)
	de.poolKeys = map[string]bool{poolKey: true}
	de.recordSessionContainer(context.Background(), poolKey, cid)

	// Mirror handleStepParked's teardown: safe-to-remove cleanup, then forget the session.
	require.NoError(t, pool.CleanupAttempt(context.Background(), poolKey, false, true))
	delete(de.sessionContainers, poolKey)

	de.Close(false)
	assert.False(t, store.row.ContainerKept)
}
