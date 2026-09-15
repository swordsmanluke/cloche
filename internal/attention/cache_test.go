package attention

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingCompute is a ComputeFunc that blocks on a channel until released,
// counting invocations and letting a test control exactly when a "fetch"
// finishes, to exercise "returns last value while a refresh is in flight".
type blockingCompute struct {
	mu      sync.Mutex
	calls   int32
	release chan struct{}
	result  []Item
}

func newBlockingCompute(result []Item) *blockingCompute {
	return &blockingCompute{release: make(chan struct{}), result: result}
}

func (b *blockingCompute) fn(ctx context.Context, projectDir string) ([]Item, error) {
	atomic.AddInt32(&b.calls, 1)
	<-b.release
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.result, nil
}

func (b *blockingCompute) unblock() {
	close(b.release)
}

func (b *blockingCompute) callCount() int32 {
	return atomic.LoadInt32(&b.calls)
}

func TestCache_GetReturnsLastValueWhileRefreshInFlight(t *testing.T) {
	bc := newBlockingCompute([]Item{{Kind: KindParked, ProjectDir: "p"}})
	c := NewCache(bc.fn, time.Hour, 1)

	// Seed an initial value directly, bypassing the blocking compute, so we
	// can tell "stale-but-present" apart from "never computed".
	c.entriesMu.Lock()
	c.entries["p"] = Snapshot{Items: []Item{{Kind: KindLongPoll, ProjectDir: "p"}}, ComputedAt: time.Now()}
	c.entriesMu.Unlock()

	c.Refresh("p")

	// Wait until the compute is actually running, then check Get doesn't
	// block and still reports the old value.
	for bc.callCount() == 0 {
		time.Sleep(time.Millisecond)
	}
	snap := c.Get("p")
	if len(snap.Items) != 1 || snap.Items[0].Kind != KindLongPoll {
		t.Fatalf("Get during in-flight refresh = %+v, want the stale long-poll item", snap.Items)
	}

	bc.unblock()

	deadline := time.Now().Add(time.Second)
	for {
		snap = c.Get("p")
		if len(snap.Items) == 1 && snap.Items[0].Kind == KindParked {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Get after refresh = %+v, want the new parked item", snap.Items)
		}
		time.Sleep(time.Millisecond)
	}
	if snap.ComputedAt.IsZero() {
		t.Fatalf("ComputedAt is zero after a completed refresh")
	}
}

func TestCache_NeverRefreshedReportsZeroSnapshot(t *testing.T) {
	c := NewCache(func(ctx context.Context, projectDir string) ([]Item, error) {
		t.Fatalf("compute should not be called by Get")
		return nil, nil
	}, time.Hour, 1)

	snap := c.Get("unknown-project")
	if len(snap.Items) != 0 {
		t.Fatalf("Items = %+v, want empty", snap.Items)
	}
	if !snap.ComputedAt.IsZero() {
		t.Fatalf("ComputedAt = %v, want zero", snap.ComputedAt)
	}
}

func TestCache_RefreshTriggersRecompute(t *testing.T) {
	var calls int32
	c := NewCache(func(ctx context.Context, projectDir string) ([]Item, error) {
		atomic.AddInt32(&calls, 1)
		return []Item{{Kind: KindStaleClaim, TaskID: "task-1"}}, nil
	}, time.Hour, 1)

	if got := c.Get("p"); len(got.Items) != 0 {
		t.Fatalf("expected empty snapshot before any refresh, got %+v", got.Items)
	}

	waitForRefresh(t, c, "p")
	first := c.Get("p")
	if len(first.Items) != 1 {
		t.Fatalf("expected 1 item after first refresh, got %+v", first.Items)
	}

	// A second explicit trigger (as fired by a run reaching a terminal
	// state) must recompute again rather than reuse the cached value.
	c.Refresh("p")
	waitForCallCount(t, &calls, 2)
}

// waitForRefresh blocks until projectDir has a non-zero ComputedAt, polling
// briefly — Refresh (called via Cache.Refresh in the test bodies above) is
// asynchronous by design.
func waitForRefresh(t *testing.T, c *Cache, projectDir string) {
	t.Helper()
	c.Refresh(projectDir)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !c.Get(projectDir).ComputedAt.IsZero() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("refresh for %s did not complete in time", projectDir)
}

func waitForCallCount(t *testing.T, calls *int32, want int32) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(calls) >= want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("call count = %d, want >= %d", atomic.LoadInt32(calls), want)
}

func TestCache_RunRefreshesAllProjectsOnStartAndTick(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	c := NewCache(func(ctx context.Context, projectDir string) ([]Item, error) {
		mu.Lock()
		seen[projectDir]++
		mu.Unlock()
		return nil, nil
	}, 20*time.Millisecond, 2)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Millisecond)
	defer cancel()

	c.Run(ctx, func(ctx context.Context) ([]string, error) {
		return []string{"a", "b", "c"}, nil
	})

	mu.Lock()
	defer mu.Unlock()
	for _, dir := range []string{"a", "b", "c"} {
		if seen[dir] < 2 {
			t.Errorf("project %s refreshed %d times, want at least 2 (initial + a tick)", dir, seen[dir])
		}
	}
}
