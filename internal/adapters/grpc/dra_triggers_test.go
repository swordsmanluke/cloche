package grpc

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/swordsmanluke/cloche/internal/budget"
	"github.com/swordsmanluke/cloche/internal/domain"
	"github.com/swordsmanluke/cloche/internal/dra"
	"github.com/swordsmanluke/cloche/internal/host"
	"github.com/swordsmanluke/cloche/internal/signal"
)

// wakeMessages returns the message of every wake-up the project has started.
func wakeMessages(t *testing.T, root string) []string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(root, ".cloche", "runs", "*", dra.MessageFile))
	var out []string
	for _, f := range files {
		b, err := os.ReadFile(f)
		require.NoError(t, err)
		out = append(out, string(b))
	}
	return out
}

// settle waits for running wake-ups to end.
func settle(t *testing.T, srv *ClocheServer) {
	t.Helper()
	require.Eventually(t, func() bool {
		srv.mu.Lock()
		defer srv.mu.Unlock()
		return len(srv.wakeActive) == 0
	}, 10*time.Second, 10*time.Millisecond)
}

func addDRA(t *testing.T, root, name string) {
	t.Helper()
	d := filepath.Join(root, ".cloche", "dras", name)
	require.NoError(t, os.MkdirAll(d, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(d, "charter.md"), []byte("# "+name+"\n"), 0o644))
}

func TestDRATrigger_TaskResults(t *testing.T) {
	for _, tc := range []struct {
		state domain.RunState
		want  string
	}{
		{domain.RunStateSucceeded, "task-finished"},
		{domain.RunStateFailed, "task-failed"},
		{domain.RunStateParked, "task-parked"},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			srv, root := wakeServer(t)
			srv.onTaskResult(host.TaskResult{ProjectDir: root, TaskID: "t-1", Labels: []string{"bug", "auth"}, State: tc.state})
			settle(t, srv)
			msgs := wakeMessages(t, root)
			require.Len(t, msgs, 1)
			assert.Contains(t, msgs[0], tc.want)
			assert.Contains(t, msgs[0], "t-1")
		})
	}
}

func TestDRATrigger_TaskOutsideAnyDomainOrCancelledWakesNobody(t *testing.T) {
	srv, root := wakeServer(t)
	srv.onTaskResult(host.TaskResult{ProjectDir: root, TaskID: "t-1", Labels: []string{"bug"}, State: domain.RunStateFailed})
	srv.onTaskResult(host.TaskResult{ProjectDir: root, TaskID: "t-2", Labels: []string{"auth"}, State: domain.RunStateCancelled})
	settle(t, srv)
	assert.Empty(t, wakeMessages(t, root))
}

func TestDRATrigger_TwoDomainLabelsPickFirst(t *testing.T) {
	srv, root := wakeServer(t)
	addDRA(t, root, "engine")
	srv.onTaskResult(host.TaskResult{ProjectDir: root, TaskID: "t-1", Labels: []string{"engine", "auth"}, State: domain.RunStateFailed})
	settle(t, srv)
	msgs := wakeMessages(t, root)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], `"engine"`)
	_, err := os.Stat(filepath.Join(root, ".cloche", "dras", "auth", "notes.md"))
	assert.True(t, os.IsNotExist(err), "auth must not be woken")
}

func TestDRATrigger_Signal(t *testing.T) {
	srv, root := wakeServer(t)
	srv.notifySignalDRA(root, signal.Signal{Source: "ci", Key: "k", Severity: "urgent", Title: "login is down", Domain: "auth"}, nil)
	settle(t, srv)
	msgs := wakeMessages(t, root)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], "signal")
	assert.Contains(t, msgs[0], "login is down")

	// No domain, or a domain with no DRA: nobody wakes.
	srv2, root2 := wakeServer(t)
	srv2.notifySignalDRA(root2, signal.Signal{Source: "ci", Key: "k", Title: "x"}, nil)
	srv2.notifySignalDRA(root2, signal.Signal{Source: "ci", Key: "k", Title: "x", Domain: "nope"}, nil)
	settle(t, srv2)
	assert.Empty(t, wakeMessages(t, root2))
}

func TestDRATrigger_Ruling(t *testing.T) {
	srv, root := wakeServer(t)
	require.NoError(t, srv.NotifyDRARuling(root, "auth", "CLOCHE-Q7", "Use bearer tokens."))
	settle(t, srv)
	msgs := wakeMessages(t, root)
	require.Len(t, msgs, 1)
	assert.Contains(t, msgs[0], "ruling")
	assert.Contains(t, msgs[0], "CLOCHE-Q7")
	assert.Contains(t, msgs[0], "Use bearer tokens.")
	assert.Error(t, srv.NotifyDRARuling(root, "nope", "Q", "x"))
}

func TestDRATrigger_Heartbeat(t *testing.T) {
	srv, root := wakeServer(t)
	queued, err := srv.NotifyDRAHeartbeat(root, "auth")
	require.NoError(t, err)
	assert.True(t, queued, "never woken: due")
	settle(t, srv)
	require.Len(t, wakeMessages(t, root), 1)

	// Just woken: a second heartbeat is not due, so no second wake-up.
	queued, err = srv.NotifyDRAHeartbeat(root, "auth")
	require.NoError(t, err)
	assert.False(t, queued)
	settle(t, srv)
	assert.Len(t, wakeMessages(t, root), 1)
}

func TestDRATrigger_HeartbeatIntervalSetPerProject(t *testing.T) {
	srv, root := wakeServer(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".cloche", "config.toml"), []byte("[dras]\nheartbeat = \"off\"\n"), 0o644))
	queued, err := srv.NotifyDRAHeartbeat(root, "auth")
	require.NoError(t, err)
	assert.False(t, queued, "heartbeat = off disables")
}

// Events that arrive while the DRA is awake are kept, start no second run,
// and go out together when it finishes.
func TestDRATrigger_BatchedWhileAwake(t *testing.T) {
	srv, root := wakeServer(t)
	key := wakeKey(normalizeProjectDir(root), "auth")
	srv.wakeActive = map[string]string{key: "run-1"}

	srv.onTaskResult(host.TaskResult{ProjectDir: root, TaskID: "t-1", Labels: []string{"auth"}, State: domain.RunStateFailed})
	srv.onTaskResult(host.TaskResult{ProjectDir: root, TaskID: "t-2", Labels: []string{"auth"}, State: domain.RunStateParked})
	require.NoError(t, srv.NotifyDRARuling(root, "auth", "Q1", "ruled"))
	assert.Empty(t, wakeMessages(t, root), "nothing starts a second run")
	assert.Equal(t, 3, srv.draInbox(normalizeProjectDir(root)).Pending(normalizeProjectDir(root), "auth"), "nothing is dropped")

	srv.mu.Lock()
	delete(srv.wakeActive, key)
	srv.mu.Unlock()
	srv.draWakeEnded(normalizeProjectDir(root), "auth")
	settle(t, srv)
	msgs := wakeMessages(t, root)
	require.Len(t, msgs, 1, "one wake-up carries all of them")
	for _, want := range []string{"t-1", "t-2", "Q1", "3 events"} {
		assert.Contains(t, msgs[0], want)
	}
}

// While the budget gate is closed events are kept; once it opens the retry
// delivers them together.
func TestDRATrigger_BatchedWhileBudgetClosed(t *testing.T) {
	old := draRetryInterval
	draRetryInterval = 20 * time.Millisecond
	t.Cleanup(func() { draRetryInterval = old })

	srv, root := wakeServer(t)
	r := &fixedReader{pct: 10}
	m := budget.NewMonitor(budget.Config{UsagePercentPerDay: 5}, r, "")
	m.Check(context.Background())
	r.pct = 20
	require.True(t, m.Check(context.Background()).Over)
	srv.SetBudget(m)

	srv.onTaskResult(host.TaskResult{ProjectDir: root, TaskID: "t-1", Labels: []string{"auth"}, State: domain.RunStateFailed})
	srv.onTaskResult(host.TaskResult{ProjectDir: root, TaskID: "t-2", Labels: []string{"auth"}, State: domain.RunStateFailed})
	assert.Empty(t, wakeMessages(t, root))
	p := normalizeProjectDir(root)
	assert.Equal(t, 2, srv.draInbox(p).Pending(p, "auth"))

	// Usage back under the cap.
	r.pct = 10
	m2 := budget.NewMonitor(budget.Config{UsagePercentPerDay: 50}, r, "")
	m2.Check(context.Background())
	srv.SetBudget(m2)
	require.Eventually(t, func() bool { return len(wakeMessages(t, root)) == 1 }, 5*time.Second, 10*time.Millisecond)
	settle(t, srv)
	msgs := wakeMessages(t, root)
	assert.Contains(t, msgs[0], "t-1")
	assert.Contains(t, msgs[0], "t-2")
}

// A DRA's own signal does not wake it, but the result of a task it created
// does: nobody else would tell it the work failed.
func TestDRATrigger_NoSelfWake(t *testing.T) {
	srv, root := wakeServer(t)
	srv.notifySignalDRA(root, signal.Signal{Source: "dra:auth", Key: "k", Title: "mine", Domain: "auth"}, nil)
	settle(t, srv)
	assert.Empty(t, wakeMessages(t, root))

	srv.onTaskResult(host.TaskResult{ProjectDir: root, TaskID: "t-own", Labels: []string{"auth"}, State: domain.RunStateFailed})
	settle(t, srv)
	require.Len(t, wakeMessages(t, root), 1)
}
