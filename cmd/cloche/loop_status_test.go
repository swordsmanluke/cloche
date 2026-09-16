package main

import (
	"context"
	"strings"
	"testing"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
)

// TestCmdLoop_StatusSubcommandDispatchesToProjectOverview verifies that
// "cloche loop status" is parsed as the "status" subcommand (not folded into
// a single unrecognized command) and shows the project's status overview.
func TestCmdLoop_StatusSubcommandDispatchesToProjectOverview(t *testing.T) {
	client := &statusMockClient{
		projectInfoResp: &pb.GetProjectInfoResponse{
			Name:        "myproject",
			Concurrency: 2,
		},
		listRunsResp: &pb.ListRunsResponse{},
	}

	out := captureStdout(t, func() {
		cmdLoop(context.Background(), client, []string{"status"})
	})

	if !strings.Contains(out, "Project: myproject") {
		t.Errorf("expected 'loop status' to show the project overview, got:\n%s", out)
	}
}
