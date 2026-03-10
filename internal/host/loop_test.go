package host

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloche-dev/cloche/internal/domain"
)

func TestLoop_StartStop(t *testing.T) {
	store := &fakeStore{}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string) (*RunResult, error) {
		called.Add(1)
		// Simulate a run that finds no work (failed = aborted).
		time.Sleep(50 * time.Millisecond)
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)

	if loop.Running() {
		t.Fatal("loop should not be running before Start")
	}

	loop.Start()
	if !loop.Running() {
		t.Fatal("loop should be running after Start")
	}

	// Wait enough time for at least one run to happen.
	time.Sleep(200 * time.Millisecond)
	loop.Stop()

	if loop.Running() {
		t.Fatal("loop should not be running after Stop")
	}

	if called.Load() < 1 {
		t.Fatal("expected at least one run to be launched")
	}
}

func TestLoop_RampsUpOnSuccess(t *testing.T) {
	store := &fakeStore{}
	var called atomic.Int32

	runFn := func(ctx context.Context, projectDir string) (*RunResult, error) {
		n := called.Add(1)
		time.Sleep(20 * time.Millisecond)
		// First 3 succeed, then fail (no more work).
		if n <= 3 {
			return &RunResult{State: domain.RunStateSucceeded}, nil
		}
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)

	loop.Start()
	// With no backoff between successes, 3 successful + 1 failed should happen fast.
	time.Sleep(300 * time.Millisecond)
	loop.Stop()

	if called.Load() < 4 {
		t.Fatalf("expected at least 4 runs, got %d", called.Load())
	}
}

func TestLoop_DoubleStartIsNoop(t *testing.T) {
	store := &fakeStore{}
	runFn := func(ctx context.Context, projectDir string) (*RunResult, error) {
		time.Sleep(100 * time.Millisecond)
		return &RunResult{State: domain.RunStateFailed}, nil
	}

	loop := NewLoop(LoopConfig{
		ProjectDir:    "/tmp/test-project",
		MaxConcurrent: 1,
	}, store, runFn)

	loop.Start()
	loop.Start() // Should be a no-op.
	loop.Stop()
}
