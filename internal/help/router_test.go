package help_test

import (
	"context"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/adapters/sqlite"
	"github.com/cloche-dev/cloche/internal/help"
	"github.com/cloche-dev/cloche/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRouter(t *testing.T) (*help.Router, *sqlite.Store) {
	t.Helper()
	store, err := sqlite.NewStore(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	return help.NewRouter(store, time.Hour), store
}

func TestRouter_AskAndReply_RoundTrip(t *testing.T) {
	router, _ := newTestRouter(t)
	ctx := context.Background()

	resultCh := make(chan help.AskResult, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := router.Ask(ctx, help.AskParams{
			TaskID: "task-a", AttemptID: "att-1", RunID: "run-1", StepName: "plan",
			Channel: "cloche", Question: "Which schema should I use?",
		})
		resultCh <- res
		errCh <- err
	}()

	// Wait for the ask to be persisted and pending before replying.
	require.Eventually(t, func() bool {
		threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
		return err == nil && len(threads) == 1
	}, time.Second, 5*time.Millisecond)

	threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
	require.NoError(t, err)
	require.Len(t, threads, 1)
	thread := threads[0]
	assert.Equal(t, "cloche", thread.Channel)
	assert.NotEmpty(t, thread.Name)

	require.NoError(t, router.Reply(ctx, thread.ID, "Use schema B", "cli"))

	select {
	case res := <-resultCh:
		assert.Equal(t, "Use schema B", res.Answer)
		assert.Equal(t, thread.ID, res.ThreadID)
		assert.False(t, res.Parked)
	case <-time.After(2 * time.Second):
		t.Fatal("Ask did not return after Reply")
	}
	require.NoError(t, <-errCh)

	gotThread, msgs, err := router.GetThread(ctx, thread.ID)
	require.NoError(t, err)
	assert.Equal(t, "awaiting_agent", string(gotThread.State))
	require.Len(t, msgs, 2)
	assert.Equal(t, "Which schema should I use?", msgs[0].Body)
	assert.Equal(t, "Use schema B", msgs[1].Body)
}

func TestRouter_SecondConcurrentAskOnSameRunRejected(t *testing.T) {
	router, _ := newTestRouter(t)
	ctx := context.Background()

	firstDone := make(chan struct{})
	go func() {
		defer close(firstDone)
		router.Ask(ctx, help.AskParams{
			RunID: "run-1", Channel: "cloche", Question: "First question?",
		})
	}()

	require.Eventually(t, func() bool {
		threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
		return err == nil && len(threads) == 1
	}, time.Second, 5*time.Millisecond)

	_, err := router.Ask(ctx, help.AskParams{
		RunID: "run-1", Channel: "cloche", Question: "Second question while first is open?",
	})
	require.Error(t, err)

	threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
	require.NoError(t, err)
	require.Len(t, threads, 1)
	require.NoError(t, router.Reply(ctx, threads[0].ID, "answer", "cli"))
	<-firstDone
}

func TestRouter_FollowUpAskContinuesSameThread(t *testing.T) {
	router, _ := newTestRouter(t)
	ctx := context.Background()

	resultCh := make(chan help.AskResult, 1)
	go func() {
		res, _ := router.Ask(ctx, help.AskParams{
			RunID: "run-1", Channel: "cloche", Question: "Which schema?",
		})
		resultCh <- res
	}()
	require.Eventually(t, func() bool {
		threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
		return err == nil && len(threads) == 1
	}, time.Second, 5*time.Millisecond)
	threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
	require.NoError(t, err)
	threadID := threads[0].ID
	require.NoError(t, router.Reply(ctx, threadID, "Schema B", "cli"))
	first := <-resultCh
	require.Equal(t, threadID, first.ThreadID)

	// Follow-up ask on the same thread, after the first has resolved.
	resultCh2 := make(chan help.AskResult, 1)
	go func() {
		res, _ := router.Ask(ctx, help.AskParams{
			RunID: "run-1", Channel: "cloche", Question: "Any migrations needed?",
			ThreadID: threadID,
		})
		resultCh2 <- res
	}()
	require.Eventually(t, func() bool {
		_, msgs, err := router.GetThread(ctx, threadID)
		return err == nil && len(msgs) == 3
	}, time.Second, 5*time.Millisecond)
	require.NoError(t, router.Reply(ctx, threadID, "No", "cli"))

	select {
	case res := <-resultCh2:
		assert.Equal(t, threadID, res.ThreadID)
		assert.Equal(t, "No", res.Answer)
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up Ask did not return")
	}

	_, msgs, err := router.GetThread(ctx, threadID)
	require.NoError(t, err)
	require.Len(t, msgs, 4)

	// Only one thread was ever created — the follow-up reused it.
	all, err := router.ListThreads(ctx, ports.HelpThreadFilter{All: true})
	require.NoError(t, err)
	assert.Len(t, all, 1)
}

func TestRouter_NamingSequence(t *testing.T) {
	router, _ := newTestRouter(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		go func() {
			router.Ask(ctx, help.AskParams{
				RunID: domainRunID(i), Channel: "cloche", Question: "Which schema should I use?",
			})
		}()
	}

	require.Eventually(t, func() bool {
		threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
		return err == nil && len(threads) == 3
	}, 2*time.Second, 10*time.Millisecond)

	threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
	require.NoError(t, err)
	names := map[string]bool{}
	for _, th := range threads {
		names[th.Name] = true
		require.NoError(t, router.Reply(ctx, th.ID, "ok", "cli"))
	}
	assert.Len(t, names, 3, "expected 3 distinct thread names, got %v", names)
}

func domainRunID(i int) string {
	return "run-seq-" + string(rune('a'+i))
}

func TestRouter_ArchiveTaskThreads(t *testing.T) {
	router, _ := newTestRouter(t)
	ctx := context.Background()

	go router.Ask(ctx, help.AskParams{TaskID: "task-a", RunID: "run-1", Channel: "cloche", Question: "Q1?"})
	require.Eventually(t, func() bool {
		threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
		return err == nil && len(threads) == 1
	}, time.Second, 5*time.Millisecond)

	threads, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
	require.NoError(t, err)
	require.NoError(t, router.Reply(ctx, threads[0].ID, "answer", "cli"))

	n, err := router.ArchiveTaskThreads(ctx, "task-a")
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)

	open, err := router.ListThreads(ctx, ports.HelpThreadFilter{})
	require.NoError(t, err)
	assert.Len(t, open, 0)

	all, err := router.ListThreads(ctx, ports.HelpThreadFilter{All: true})
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "archived", string(all[0].State))
}
