package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/adapters/sqlite"
	"github.com/swordsmanluke/cloche/internal/budget"
	"github.com/swordsmanluke/cloche/internal/dra"
	"github.com/swordsmanluke/cloche/internal/ports"
)

type stubRuntime struct{ ports.ContainerRuntime }

type fixedReader struct{ pct float64 }

func (f *fixedReader) Read(context.Context) (budget.Reading, error) {
	return budget.Reading{WeeklyPercent: f.pct}, nil
}

func wakeServer(t *testing.T) (*ClocheServer, string) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	srv := NewClocheServer(store, stubRuntime{})
	root := t.TempDir()
	d := filepath.Join(root, ".cloche", "dras", "auth")
	require.NoError(t, os.MkdirAll(d, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d, "charter.md"), []byte("# Auth\n"), 0o644))
	// Wake-ups run in the background: let them finish before the store and
	// the project directory are removed.
	t.Cleanup(func() {
		require.Eventually(t, func() bool {
			srv.mu.Lock()
			defer srv.mu.Unlock()
			for _, id := range srv.wakeActive {
				if id != "run-1" {
					return false
				}
			}
			return true
		}, 10*time.Second, 10*time.Millisecond)
	})
	return srv, root
}

// A second wake while one runs is refused (not queued).
func TestWakeDRA_SecondWhileRunningIsRefused(t *testing.T) {
	srv, root := wakeServer(t)
	srv.wakeActive = map[string]string{wakeKey(normalizeProjectDir(root), "auth"): "run-1"}

	_, err := srv.WakeDRA(context.Background(), root, "auth", "again", true)
	require.ErrorIs(t, err, ErrWakeInProgress)
	assert.Contains(t, err.Error(), "run-1")
	assert.Len(t, srv.wakeActive, 1, "refused wake-up must not queue or clear the running one")
}

// The wake-up record: a wake-up creates its run (recorded, so token usage is
// kept) and releases the one-at-a-time slot when it ends, also on failure.
func TestWakeDRA_RunIsRecordedAndSlotReleased(t *testing.T) {
	srv, root := wakeServer(t)
	runID, err := srv.WakeDRA(context.Background(), root, "auth", "main is red", true)
	require.NoError(t, err)
	require.Contains(t, runID, "dra-wake-auth")

	// No container pool in this test, so the container step fails fast.
	require.Eventually(t, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.wakeActive) == 0
	}, 10*time.Second, 20*time.Millisecond)

	run, err := srv.store.GetRun(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, "dra-wake-auth", run.WorkflowName)
	msg, err := os.ReadFile(filepath.Join(root, ".cloche", "runs", runID, dra.MessageFile))
	require.NoError(t, err)
	assert.Contains(t, string(msg), "main is red")
	// notes.md exists for the container to receive.
	_, err = os.Stat(filepath.Join(root, ".cloche", "dras", "auth", "notes.md"))
	assert.NoError(t, err)

	require.Eventually(t, func() bool {
		s, _ := dra.LoadSessions(root)
		return s["auth"].LastResult != "running" && s["auth"].LastResult != ""
	}, 10*time.Second, 20*time.Millisecond)
	s, _ := dra.LoadSessions(root)
	assert.Equal(t, runID, s["auth"].LastRunID)

	_, err = srv.WakeDRA(context.Background(), root, "auth", "next", true)
	assert.NoError(t, err, "slot is free again")
}

// The budget launch gate applies to automatic wake-ups; a hand-started one
// still goes (the RunWorkflow wrapper adds the warning).
func TestWakeDRA_BudgetGate(t *testing.T) {
	srv, root := wakeServer(t)
	r := &fixedReader{pct: 10}
	m := budget.NewMonitor(budget.Config{UsagePercentPerDay: 5}, r, "")
	m.Check(context.Background())
	r.pct = 20
	require.True(t, m.Check(context.Background()).Over)
	srv.SetBudget(m)

	_, err := srv.WakeDRA(context.Background(), root, "auth", "heartbeat", false)
	require.ErrorIs(t, err, ErrBudgetBlocked)

	_, err = srv.WakeDRA(context.Background(), root, "auth", "by hand", true)
	require.NoError(t, err)
}

func TestWakeDRA_Validation(t *testing.T) {
	srv, root := wakeServer(t)
	_, err := srv.WakeDRA(context.Background(), root, "nope", "x", true)
	assert.Error(t, err)
	_, err = srv.WakeDRA(context.Background(), root, "auth", "", true)
	assert.Error(t, err)
}
