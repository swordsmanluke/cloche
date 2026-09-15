package main

import (
	"context"
	"testing"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"google.golang.org/grpc"
)

// threadsMockClient implements the gRPC methods needed by threads commands.
type threadsMockClient struct {
	pb.ClocheServiceClient

	listThreadsResp *pb.ListThreadsResponse
}

func (m *threadsMockClient) ListThreads(_ context.Context, _ *pb.ListThreadsRequest, _ ...grpc.CallOption) (*pb.ListThreadsResponse, error) {
	if m.listThreadsResp != nil {
		return m.listThreadsResp, nil
	}
	return &pb.ListThreadsResponse{}, nil
}

// TestCmdThreadsList_NoColorFlag verifies that "--no-color" is accepted by
// "cloche threads list" instead of being rejected as an unknown flag.
// --no-color is pre-scanned globally in main() (it never reaches per-command
// flag parsing as a value to consume), so every command that parses its own
// flags strictly must tolerate it explicitly.
func TestCmdThreadsList_NoColorFlag(t *testing.T) {
	client := &threadsMockClient{listThreadsResp: &pb.ListThreadsResponse{}}

	output := captureStdout(t, func() {
		cmdThreadsList(context.Background(), client, []string{"--no-color"})
	})

	if output != "No open threads.\n" {
		t.Errorf("expected clean 'No open threads.' output, got %q", output)
	}
}

// TestCmdThreads_BareNoColorFlag verifies "cloche threads --no-color" (no
// explicit "list" verb) also works, routing through the same flag parsing.
func TestCmdThreads_BareNoColorFlag(t *testing.T) {
	client := &threadsMockClient{listThreadsResp: &pb.ListThreadsResponse{}}

	output := captureStdout(t, func() {
		cmdThreads(context.Background(), client, []string{"--no-color"})
	})

	if output != "No open threads.\n" {
		t.Errorf("expected clean 'No open threads.' output, got %q", output)
	}
}
