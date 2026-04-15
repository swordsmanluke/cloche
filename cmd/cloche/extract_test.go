package main

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"google.golang.org/grpc"
)

// mockExtractClient captures ExtractRun calls for test verification.
type mockExtractClient struct {
	pb.ClocheServiceClient

	mu       sync.Mutex
	requests []*pb.ExtractRunRequest
	response *pb.ExtractRunResponse
	err      error
}

func newMockExtractClient(resp *pb.ExtractRunResponse, err error) *mockExtractClient {
	return &mockExtractClient{response: resp, err: err}
}

func (m *mockExtractClient) ExtractRun(_ context.Context, req *pb.ExtractRunRequest, _ ...grpc.CallOption) (*pb.ExtractRunResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, req)
	return m.response, m.err
}

// lastRequest returns the most recent ExtractRunRequest received by the mock.
func (m *mockExtractClient) lastRequest() *pb.ExtractRunRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.requests) == 0 {
		return nil
	}
	return m.requests[len(m.requests)-1]
}

// TestExtractRun_FlagParsing_At verifies --at flag populates AtDir in the request.
func TestExtractRun_FlagParsing_At(t *testing.T) {
	mock := newMockExtractClient(&pb.ExtractRunResponse{
		TargetDir: "/tmp/mydir",
		Branch:    "cloche/run123",
	}, nil)

	var stdout, stderr bytes.Buffer
	code := extractRun(context.Background(), mock, "run123", "/tmp/mydir", "", false, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	req := mock.lastRequest()
	if req == nil {
		t.Fatal("expected ExtractRun to be called")
	}
	if req.AtDir != "/tmp/mydir" {
		t.Errorf("expected AtDir=%q, got %q", "/tmp/mydir", req.AtDir)
	}
}

// TestExtractRun_FlagParsing_Branch verifies --branch flag populates Branch in the request.
func TestExtractRun_FlagParsing_Branch(t *testing.T) {
	mock := newMockExtractClient(&pb.ExtractRunResponse{
		TargetDir: "/workspace/.gitworktrees/cloche/run123",
		Branch:    "fix/foo",
	}, nil)

	var stdout, stderr bytes.Buffer
	code := extractRun(context.Background(), mock, "run123", "", "fix/foo", false, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	req := mock.lastRequest()
	if req == nil {
		t.Fatal("expected ExtractRun to be called")
	}
	if req.Branch != "fix/foo" {
		t.Errorf("expected Branch=%q, got %q", "fix/foo", req.Branch)
	}
}

// TestExtractRun_FlagParsing_NoGit verifies --no-git flag populates NoGit in the request.
func TestExtractRun_FlagParsing_NoGit(t *testing.T) {
	mock := newMockExtractClient(&pb.ExtractRunResponse{
		TargetDir: "/tmp/inspect",
		Branch:    "", // empty when no-git
	}, nil)

	var stdout, stderr bytes.Buffer
	code := extractRun(context.Background(), mock, "run123", "/tmp/inspect", "", true, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	req := mock.lastRequest()
	if req == nil {
		t.Fatal("expected ExtractRun to be called")
	}
	if !req.NoGit {
		t.Error("expected NoGit=true in request")
	}
}

// TestExtractRun_SuccessOutput verifies the printed output includes target dir and branch.
func TestExtractRun_SuccessOutput(t *testing.T) {
	mock := newMockExtractClient(&pb.ExtractRunResponse{
		TargetDir: "/workspace/.gitworktrees/cloche/abc123",
		Branch:    "cloche/abc123",
		CommitSha: "deadbeef",
	}, nil)

	var stdout, stderr bytes.Buffer
	code := extractRun(context.Background(), mock, "abc123", "", "", false, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Extracted to: /workspace/.gitworktrees/cloche/abc123") {
		t.Errorf("expected target dir in output, got:\n%s", out)
	}
	if !strings.Contains(out, "Branch: cloche/abc123") {
		t.Errorf("expected branch in output, got:\n%s", out)
	}
}

// TestExtractRun_NoGitOutput verifies that no "Branch:" line is printed when NoGit is true.
func TestExtractRun_NoGitOutput(t *testing.T) {
	mock := newMockExtractClient(&pb.ExtractRunResponse{
		TargetDir: "/tmp/inspect",
		Branch:    "", // empty when no-git
	}, nil)

	var stdout, stderr bytes.Buffer
	code := extractRun(context.Background(), mock, "abc123", "/tmp/inspect", "", true, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "Extracted to: /tmp/inspect") {
		t.Errorf("expected target dir in output, got:\n%s", out)
	}
	if strings.Contains(out, "Branch:") {
		t.Errorf("expected no Branch line when no-git, got:\n%s", out)
	}
}

// TestExtractRun_Error verifies that RPC errors are reported to stderr and exit code is 1.
func TestExtractRun_Error(t *testing.T) {
	mock := newMockExtractClient(nil, fmt.Errorf("run %q not found", "badid"))

	var stdout, stderr bytes.Buffer
	code := extractRun(context.Background(), mock, "badid", "", "", false, &stdout, &stderr)

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "cloche extract:") {
		t.Errorf("expected error prefix in stderr, got: %s", stderr.String())
	}
	if stdout.Len() > 0 {
		t.Errorf("expected no stdout on error, got: %s", stdout.String())
	}
}

// TestExtractRun_AllFlagsCombined verifies that all flags together arrive in the request.
func TestExtractRun_AllFlagsCombined(t *testing.T) {
	mock := newMockExtractClient(&pb.ExtractRunResponse{
		TargetDir: "/tmp/combined",
		Branch:    "", // no-git: branch empty
	}, nil)

	var stdout, stderr bytes.Buffer
	code := extractRun(context.Background(), mock, "run999", "/tmp/combined", "feat/test", true, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", code, stderr.String())
	}
	req := mock.lastRequest()
	if req.Id != "run999" {
		t.Errorf("expected Id=%q, got %q", "run999", req.Id)
	}
	if req.AtDir != "/tmp/combined" {
		t.Errorf("expected AtDir=%q, got %q", "/tmp/combined", req.AtDir)
	}
	if req.Branch != "feat/test" {
		t.Errorf("expected Branch=%q, got %q", "feat/test", req.Branch)
	}
	if !req.NoGit {
		t.Error("expected NoGit=true in request")
	}
}

// TestExtractHelpText verifies the extract subcommand has help text with required content.
func TestExtractHelpText(t *testing.T) {
	text, ok := subcommandHelp["extract"]
	if !ok {
		t.Fatal("missing help text for extract subcommand")
	}
	if !strings.Contains(text, "--at") {
		t.Error("extract help should mention --at flag")
	}
	if !strings.Contains(text, "--no-git") {
		t.Error("extract help should mention --no-git flag")
	}
	if !strings.Contains(text, "--branch") {
		t.Error("extract help should mention --branch flag")
	}
	if !strings.Contains(text, "keep-container") {
		t.Error("extract help should mention --keep-container")
	}
	if !strings.Contains(text, "Extracted to:") {
		t.Error("extract help should show output format")
	}
}
