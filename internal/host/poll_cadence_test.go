package host

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakePollRunner is a stub poll script: it records the fake time of each run
// and always reports "pending" (empty result).
type fakePollRunner struct {
	clock *FakePollClock
	mu    sync.Mutex
	runs  []time.Time
}

func (r *fakePollRunner) invoke(ctx context.Context) (string, error) {
	r.mu.Lock()
	r.runs = append(r.runs, r.clock.Now())
	r.mu.Unlock()
	return "", nil
}

func (r *fakePollRunner) runTimes() []time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]time.Time(nil), r.runs...)
}

// waitIdle blocks until no poll invocation is in flight, so the fake-clock
// simulation stays deterministic.
func waitIdle(t *testing.T, c *PollCoordinator) {
	t.Helper()
	require.Eventually(t, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, s := range c.sessions {
			if s.inFlight {
				return false
			}
		}
		return true
	}, 2*time.Second, time.Millisecond)
}

// simulatePolls runs a poll with the given interval for total fake time while
// the orchestration loop ticks every loopTick, and returns the fake times the
// poll script ran at.
func simulatePolls(t *testing.T, interval, loopTick, total time.Duration) []time.Time {
	t.Helper()
	clock := NewFakePollClock()
	coord := NewPollCoordinator()
	coord.SetClock(clock)
	clock.Settle = func() { waitIdle(t, coord) }
	runner := &fakePollRunner{clock: clock}
	coord.Register("run1", "wait", runner.invoke, interval)

	coord.DrivePolls(context.Background(), nil) // tick at t=0
	waitIdle(t, coord)
	for elapsed := time.Duration(0); elapsed < total; elapsed += loopTick {
		clock.Advance(loopTick)
		coord.DrivePolls(context.Background(), nil)
		waitIdle(t, coord)
	}
	return runner.runTimes()
}

func maxGap(runs []time.Time) time.Duration {
	var m time.Duration
	for i := 1; i < len(runs); i++ {
		if g := runs[i].Sub(runs[i-1]); g > m {
			m = g
		}
	}
	return m
}

// TestPollCadence_LoopTickSlowerThanInterval is the acceptance test for the
// poll-interval contract: interval=30s must poll about every 30s even when the
// orchestration loop only ticks every 60s.
//
// Before the fix DrivePolls was the only thing firing polls, so the effective
// cadence was the interval rounded up to a multiple of the loop tick.
//
// The coordinator arms a per-session timer for the next due poll, so the
// cadence no longer depends on the loop tick.
func TestPollCadence_LoopTickSlowerThanInterval(t *testing.T) {
	const interval, tolerance = 30 * time.Second, 2 * time.Second
	runs := simulatePolls(t, interval, 60*time.Second, 5*time.Minute)
	require.NotEmpty(t, runs)
	require.LessOrEqual(t, maxGap(runs), interval+tolerance, "runs=%d", len(runs))
	require.GreaterOrEqual(t, len(runs), 10)
}

// TestPollCadence_LoopTickFasterThanInterval guards the other direction: a
// fast loop must never poll more often than once per interval.
func TestPollCadence_LoopTickFasterThanInterval(t *testing.T) {
	const interval = 30 * time.Second
	runs := simulatePolls(t, interval, 5*time.Second, 5*time.Minute)
	for i := 1; i < len(runs); i++ {
		require.GreaterOrEqual(t, runs[i].Sub(runs[i-1]), interval)
	}
	require.GreaterOrEqual(t, len(runs), 9)
}
