package main

import (
	"context"
	"fmt"
	"io"

	pb "github.com/cloche-dev/cloche/api/clochepb"
)

// QuiesceRunner abstracts the quiesce operation so tests can inject a fake.
type QuiesceRunner interface {
	QuiesceRuns(ctx context.Context, projectDir string) (int32, error)
}

// grpcQuiesceClient calls the real daemon QuiesceRuns RPC.
type grpcQuiesceClient struct {
	client pb.ClocheServiceClient
}

func (g *grpcQuiesceClient) QuiesceRuns(ctx context.Context, projectDir string) (int32, error) {
	resp, err := g.client.QuiesceRuns(ctx, &pb.QuiesceRunsRequest{ProjectDir: projectDir})
	if err != nil {
		return 0, err
	}
	return resp.ParkedCount, nil
}

// newQuiesceRunner returns a real gRPC-backed quiesce runner.
func newQuiesceRunner(client pb.ClocheServiceClient) QuiesceRunner {
	return &grpcQuiesceClient{client: client}
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
