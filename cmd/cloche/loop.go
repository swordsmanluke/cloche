package main

import (
	"context"
	"fmt"
	"io"
)

// QuiesceRunner abstracts the quiesce operation so L1 can use a mock
// and L2 can replace it with a real daemon RPC.
type QuiesceRunner interface {
	QuiesceRuns(ctx context.Context, projectDir string) (int32, error)
}

// mockQuiesceClient is the L1 stub. It returns fakeResumableCount without
// contacting the daemon. TODO: L2 replaces this with a gRPC call.
type mockQuiesceClient struct {
	fakeResumableCount int32
}

func (m *mockQuiesceClient) QuiesceRuns(_ context.Context, _ string) (int32, error) {
	return m.fakeResumableCount, nil
}

// newQuiesceRunner returns the L1 mock quiesce runner.
// TODO: L2 will pass a real daemon client here instead.
func newQuiesceRunner() QuiesceRunner {
	return &mockQuiesceClient{fakeResumableCount: 0}
}

// cmdLoopQuiesce parks all resumable runs so they do not fire on daemon restart.
func cmdLoopQuiesce(ctx context.Context, runner QuiesceRunner, projectDir string, w io.Writer) error {
	count, err := runner.QuiesceRuns(ctx, projectDir)
	if err != nil {
		return fmt.Errorf("quiesce: %w", err)
	}
	fmt.Fprintf(w, "%d resumable runs parked\n", count)
	return nil
}

// fakeResumableCount returns a stub count of resumable runs for display in
// "cloche loop status". TODO: L2 replaces this with a real daemon query.
var fakeResumableCount = func() int32 { return 0 }
