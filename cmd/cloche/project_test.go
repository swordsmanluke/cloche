package main

import (
	"bytes"
	"context"
	"net"
	"strings"
	"testing"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"google.golang.org/grpc"
)

// projectInfoServer implements just GetProjectInfo, enough to stand in for
// the daemon's gRPC listener in tests of project command dispatch.
type projectInfoServer struct {
	pb.UnimplementedClocheServiceServer
	resp *pb.GetProjectInfoResponse
}

func (s *projectInfoServer) GetProjectInfo(context.Context, *pb.GetProjectInfoRequest) (*pb.GetProjectInfoResponse, error) {
	return s.resp, nil
}

// TestProjectCommand_ReposListDispatchesToReposTable verifies that "cloche
// project repos list" is parsed as the "repos list" subcommand (not folded
// into a single unrecognized command) and renders the repos table instead of
// the full project info block.
func TestProjectCommand_ReposListDispatchesToReposTable(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterClocheServiceServer(grpcServer, &projectInfoServer{resp: &pb.GetProjectInfoResponse{
		Name: "myproject",
		Repositories: []*pb.Repository{
			{Name: "main", Path: "."},
		},
	}})
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	var buf bytes.Buffer
	if err := projectCommand([]string{"repos", "list"}, lis.Addr().String(), &buf); err != nil {
		t.Fatalf("projectCommand: %v", err)
	}

	out := buf.String()
	if strings.Contains(out, "Project:") {
		t.Errorf("expected 'repos list' to render only the repos table, got:\n%s", out)
	}
	if !strings.Contains(out, "main") {
		t.Errorf("expected repo name in output, got:\n%s", out)
	}
}

func TestProjectHelpExists(t *testing.T) {
	text, ok := subcommandHelp["project"]
	if !ok {
		t.Fatal("missing help text for project subcommand")
	}
	if !strings.Contains(text, "Usage:") {
		t.Error("project help missing Usage: section")
	}
	if !strings.Contains(text, "Examples:") {
		t.Error("project help missing Examples: section")
	}
	if !strings.Contains(text, "--name") {
		t.Error("project help should mention --name flag")
	}
}

func TestProjectHelpInTopLevel(t *testing.T) {
	// The subcommandHelp map should have a project entry.
	if _, ok := subcommandHelp["project"]; !ok {
		t.Error("project subcommand missing from help registry")
	}
}
