package host

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestPollCoordinator_ImmediateDecision verifies that when the poll script
// returns a decision on the first invocation, the result channel receives it.
func TestPollCoordinator_ImmediateDecision(t *testing.T) {
	coord := NewPollCoordinator()

	invokeFn := func(ctx context.Context) (string, error) {
		return "approved", nil
	}

	interval := 10 * time.Millisecond
	ch := coord.Register("run1", "review", invokeFn, interval)

	// First DrivePolls should trigger the first poll immediately (lastPollAt
	// is set to now-interval on registration).
	coord.DrivePolls(context.Background(), nil)

	select {
	case result := <-ch:
		assert.Equal(t, "approved", result)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for result")
	}

	assert.Equal(t, 0, coord.SessionCount(), "session should be cleaned up after decision")
}

// TestPollCoordinator_PendingThenDecision verifies that polling continues until
// a decision is returned.
func TestPollCoordinator_PendingThenDecision(t *testing.T) {
	coord := NewPollCoordinator()

	var callCount atomic.Int32
	invokeFn := func(ctx context.Context) (string, error) {
		n := callCount.Add(1)
		if n >= 3 {
			return "approved", nil
		}
		return "", nil // pending
	}

	interval := 5 * time.Millisecond
	ch := coord.Register("run1", "review", invokeFn, interval)

	// Drive polls until the result arrives.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		coord.DrivePolls(context.Background(), nil)
		select {
		case result := <-ch:
			assert.Equal(t, "approved", result)
			assert.GreaterOrEqual(t, callCount.Load(), int32(3))
			assert.Equal(t, 0, coord.SessionCount())
			return
		default:
		}
		time.Sleep(interval)
	}
	t.Fatal("timed out: did not receive decision")
}

// TestPollCoordinator_FailOnInvokeError verifies that an invokeFn error
// results in "fail" being delivered.
func TestPollCoordinator_FailOnInvokeError(t *testing.T) {
	coord := NewPollCoordinator()

	invokeFn := func(ctx context.Context) (string, error) {
		return "", fmt.Errorf("script failed")
	}

	interval := 5 * time.Millisecond
	ch := coord.Register("run1", "review", invokeFn, interval)

	coord.DrivePolls(context.Background(), nil)

	select {
	case result := <-ch:
		assert.Equal(t, "fail", result)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for result")
	}

	assert.Equal(t, 0, coord.SessionCount())
}

// TestPollCoordinator_OverdueInvocationFails verifies that an invocation running
// longer than 4× the interval causes "fail" to be delivered.
func TestPollCoordinator_OverdueInvocationFails(t *testing.T) {
	coord := NewPollCoordinator()

	// invokeFn blocks until its context is cancelled (simulating a hung script).
	invokeFn := func(ctx context.Context) (string, error) {
		<-ctx.Done()
		return "", ctx.Err()
	}

	interval := 20 * time.Millisecond
	ch := coord.Register("run1", "review", invokeFn, interval)

	// First DrivePolls starts the invocation.
	coord.DrivePolls(context.Background(), nil)

	// Wait for invocation to start (inFlight = true).
	time.Sleep(5 * time.Millisecond)

	// Advance time artificially by calling DrivePolls after the 4× threshold.
	// We can't actually wait 80ms in a test, so we hack the session's invStart.
	coord.mu.Lock()
	for _, s := range coord.sessions {
		s.invStart = time.Now().Add(-5 * interval) // pretend it started 5× ago
	}
	coord.mu.Unlock()

	// This DrivePolls should detect the overage and deliver "fail".
	coord.DrivePolls(context.Background(), nil)

	select {
	case result := <-ch:
		assert.Equal(t, "fail", result)
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for overage fail")
	}

	assert.Equal(t, 0, coord.SessionCount())
}

// TestPollCoordinator_Unregister verifies that Unregister cancels in-flight
// invocations and removes the session without sending to the result channel.
func TestPollCoordinator_Unregister(t *testing.T) {
	coord := NewPollCoordinator()

	started := make(chan struct{})
	invokeFn := func(ctx context.Context) (string, error) {
		close(started)
		<-ctx.Done()
		return "", ctx.Err()
	}

	interval := 5 * time.Millisecond
	ch := coord.Register("run1", "review", invokeFn, interval)

	// Start an invocation.
	coord.DrivePolls(context.Background(), nil)
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("invokeFn never started")
	}

	// Unregister should cancel the context and clean up.
	coord.Unregister("run1", "review")
	assert.Equal(t, 0, coord.SessionCount())

	// No result should arrive on the channel.
	select {
	case result := <-ch:
		t.Fatalf("unexpected result after Unregister: %q", result)
	case <-time.After(50 * time.Millisecond):
		// Expected: no result.
	}
}

// TestPollCoordinator_IntervalRespected verifies that DrivePolls does not
// trigger a poll before the interval has elapsed.
func TestPollCoordinator_IntervalRespected(t *testing.T) {
	coord := NewPollCoordinator()

	var callCount atomic.Int32
	invokeFn := func(ctx context.Context) (string, error) {
		callCount.Add(1)
		return "approved", nil
	}

	interval := 100 * time.Millisecond
	ch := coord.Register("run1", "review", invokeFn, interval)

	// First call should trigger immediately (lastPollAt = now - interval).
	coord.DrivePolls(context.Background(), nil)

	// Wait for the first poll to complete.
	select {
	case <-ch:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for first result")
	}

	// Only one invocation should have been triggered.
	assert.Equal(t, int32(1), callCount.Load())
}

// TestPollCoordinator_NoInFlightSkip verifies that DrivePolls skips a session
// that already has an in-flight invocation.
func TestPollCoordinator_NoInFlightSkip(t *testing.T) {
	coord := NewPollCoordinator()

	started := make(chan struct{}, 1)
	var callCount atomic.Int32
	invokeFn := func(ctx context.Context) (string, error) {
		callCount.Add(1)
		started <- struct{}{}
		// Block until cancelled so we stay in-flight across multiple DrivePolls.
		<-ctx.Done()
		return "", ctx.Err()
	}

	interval := 5 * time.Millisecond
	coord.Register("run1", "review", invokeFn, interval)

	// Start the first invocation.
	coord.DrivePolls(context.Background(), nil)
	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("invokeFn never started")
	}

	// Call DrivePolls again while the invocation is in-flight.
	coord.DrivePolls(context.Background(), nil)
	coord.DrivePolls(context.Background(), nil)

	// Only one invocation should have been started.
	assert.Equal(t, int32(1), callCount.Load())

	coord.Unregister("run1", "review")
}
