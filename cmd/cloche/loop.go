package main

import (
	"context"
	"fmt"
	"io"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

// HardStopRunner abstracts the hard-stop operation (shutting down in-flight /
// resumable runs) so tests can inject a fake.
type HardStopRunner interface {
	ShutDownRuns(ctx context.Context, projectDir string) (int32, error)
}

// grpcHardStopClient calls the daemon to shut down resumable runs. The RPC is
// still named QuiesceRuns on the wire (proto), but the operator-facing command
// is `cloche loop stop --hard`.
type grpcHardStopClient struct {
	client pb.ClocheServiceClient
}

func (g *grpcHardStopClient) ShutDownRuns(ctx context.Context, projectDir string) (int32, error) {
	resp, err := g.client.QuiesceRuns(ctx, &pb.QuiesceRunsRequest{ProjectDir: projectDir})
	if err != nil {
		return 0, err
	}
	return resp.ParkedCount, nil
}

// newHardStopRunner returns a real gRPC-backed hard-stop runner.
func newHardStopRunner(client pb.ClocheServiceClient) HardStopRunner {
	return &grpcHardStopClient{client: client}
}

// cmdLoopHardStop shuts down in-flight / resumable runs so they do not
// auto-resume on the next daemon restart. Used by `cloche loop stop --hard`.
func cmdLoopHardStop(ctx context.Context, runner HardStopRunner, projectDir string, w io.Writer) error {
	count, err := runner.ShutDownRuns(ctx, projectDir)
	if err != nil {
		return fmt.Errorf("hard stop: %w", err)
	}
	fmt.Fprintf(w, "Shut down %d running task(s) (parked; will not resume on restart)\n", count)
	return nil
}
