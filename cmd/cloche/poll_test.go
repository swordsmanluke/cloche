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

// mockClocheClient implements pb.ClocheServiceClient for testing poll logic.
type mockClocheClient struct {
	pb.ClocheServiceClient

	mu        sync.Mutex
	statuses  map[string][]*pb.GetStatusResponse
	callCount map[string]int
}

func newMockClient() *mockClocheClient {
	return &mockClocheClient{
		statuses:  make(map[string][]*pb.GetStatusResponse),
		callCount: make(map[string]int),
	}
}

func (m *mockClocheClient) GetStatus(_ context.Context, req *pb.GetStatusRequest, _ ...grpc.CallOption) (*pb.GetStatusResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Prefer the Id field over RunId for lookup.
	key := req.Id
	if key == "" {
		key = req.RunId
	}

	resps, ok := m.statuses[key]
	if !ok {
		return nil, fmt.Errorf("unknown run: %s", key)
	}

	idx := m.callCount[key]
	if idx >= len(resps) {
		idx = len(resps) - 1
	}
	m.callCount[key] = idx + 1

	return resps[idx], nil
}

func TestIsTerminalState(t *testing.T) {
	tests := []struct {
		state string
		want  bool
	}{
		{"succeeded", true},
		{"failed", true},
		{"cancelled", true},
		{"running", false},
		{"pending", false},
		{"", false},
	}

	for _, tt := range tests {
		got := isTerminalState(tt.state)
		if got != tt.want {
			t.Errorf("isTerminalState(%q) = %v, want %v", tt.state, got, tt.want)
		}
	}
}

func TestCmdPollMulti_AllSucceeded(t *testing.T) {
	client := newMockClient()
	client.statuses["run1"] = []*pb.GetStatusResponse{
		{RunId: "run1", State: "running"},
		{RunId: "run1", State: "succeeded"},
	}
	client.statuses["run2"] = []*pb.GetStatusResponse{
		{RunId: "run2", State: "running"},
		{RunId: "run2", State: "running"},
		{RunId: "run2", State: "succeeded"},
	}

	var stdout, stderr bytes.Buffer
	exitCode := cmdPollMulti(client, []string{"run1", "run2"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}

	output := stdout.String()
	if !strings.Contains(output, "run1: ") {
		t.Error("output should contain status for run1")
	}
	if !strings.Contains(output, "run2: ") {
		t.Error("output should contain status for run2")
	}
	if !strings.Contains(output, "succeeded") {
		t.Error("output should show succeeded state")
	}
}

func TestCmdPollMulti_OneFailed(t *testing.T) {
	client := newMockClient()
	client.statuses["run1"] = []*pb.GetStatusResponse{
		{RunId: "run1", State: "succeeded"},
	}
	client.statuses["run2"] = []*pb.GetStatusResponse{
		{RunId: "run2", State: "failed"},
	}

	var stdout, stderr bytes.Buffer
	exitCode := cmdPollMulti(client, []string{"run1", "run2"}, &stdout, &stderr)

	if exitCode != 1 {
		t.Errorf("expected exit code 1 when a run failed, got %d", exitCode)
	}
}

func TestCmdPollMulti_OneCancelled(t *testing.T) {
	client := newMockClient()
	client.statuses["run1"] = []*pb.GetStatusResponse{
		{RunId: "run1", State: "succeeded"},
	}
	client.statuses["run2"] = []*pb.GetStatusResponse{
		{RunId: "run2", State: "cancelled"},
	}

	var stdout, stderr bytes.Buffer
	exitCode := cmdPollMulti(client, []string{"run1", "run2"}, &stdout, &stderr)

	if exitCode != 1 {
		t.Errorf("expected exit code 1 when a run was cancelled, got %d", exitCode)
	}
}

func TestCmdPollMulti_ErrorState(t *testing.T) {
	client := newMockClient()
	client.statuses["run1"] = []*pb.GetStatusResponse{
		{RunId: "run1", State: "succeeded"},
	}
	// run2 is not in the mock, so GetStatus returns an error

	var stdout, stderr bytes.Buffer
	exitCode := cmdPollMulti(client, []string{"run1", "run2"}, &stdout, &stderr)

	if exitCode != 1 {
		t.Errorf("expected exit code 1 when a run has error, got %d", exitCode)
	}
	errOutput := stderr.String()
	if !strings.Contains(errOutput, "error polling run2") {
		t.Errorf("expected error message for run2, got: %s", errOutput)
	}
}

func TestCmdPollMulti_OutputFormat(t *testing.T) {
	client := newMockClient()
	client.statuses["aaa"] = []*pb.GetStatusResponse{
		{RunId: "aaa", State: "running"},
		{RunId: "aaa", State: "succeeded"},
	}
	client.statuses["bbb"] = []*pb.GetStatusResponse{
		{RunId: "bbb", State: "succeeded"},
	}

	var stdout, stderr bytes.Buffer
	_ = cmdPollMulti(client, []string{"aaa", "bbb"}, &stdout, &stderr)

	output := stdout.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	foundOrder := false
	for i, line := range lines {
		if strings.HasPrefix(line, "aaa: ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "bbb: ") {
			foundOrder = true
			break
		}
	}
	if !foundOrder {
		t.Errorf("expected runs to be printed in argument order (aaa then bbb), got:\n%s", output)
	}
}

func TestCmdPollMulti_OnlyPrintsOnChange(t *testing.T) {
	client := newMockClient()
	// Two polls with same state, then terminal
	client.statuses["run1"] = []*pb.GetStatusResponse{
		{RunId: "run1", State: "running"},
		{RunId: "run1", State: "running"},
		{RunId: "run1", State: "succeeded"},
	}

	var stdout, stderr bytes.Buffer
	_ = cmdPollMulti(client, []string{"run1"}, &stdout, &stderr)

	output := stdout.String()
	count := strings.Count(output, "run1: running")
	if count != 1 {
		t.Errorf("expected 'run1: running' to appear once (only on change), appeared %d times in:\n%s", count, output)
	}
}

func TestExtractStepName(t *testing.T) {
	tests := []struct {
		id   string
		want string
	}{
		{"a133", ""},
		{"a133:develop", ""},
		{"a133:develop:review", "review"},
		{"shandalar-1234:a133:review", "review"},
		{"abc", ""},
		{"abc:def:ghi", "ghi"},
	}
	for _, tt := range tests {
		got := extractStepName(tt.id)
		if got != tt.want {
			t.Errorf("extractStepName(%q) = %q, want %q", tt.id, got, tt.want)
		}
	}
}

func TestCmdPollMulti_UsesIdField(t *testing.T) {
	client := newMockClient()
	// Keys use the full ID as passed (simulating workflow IDs).
	client.statuses["a133:develop"] = []*pb.GetStatusResponse{
		{RunId: "run1", State: "running"},
		{RunId: "run1", State: "succeeded"},
	}

	var stdout, stderr bytes.Buffer
	exitCode := cmdPollMulti(client, []string{"a133:develop"}, &stdout, &stderr)

	if exitCode != 0 {
		t.Errorf("expected exit code 0, got %d", exitCode)
	}
	if !strings.Contains(stdout.String(), "succeeded") {
		t.Error("expected succeeded in output")
	}
}

func TestPollHelpText(t *testing.T) {
	text, ok := subcommandHelp["poll"]
	if !ok {
		t.Fatal("missing help text for poll subcommand")
	}
	if !strings.Contains(text, "id...") {
		t.Error("poll help should mention multiple IDs")
	}
	if !strings.Contains(text, "multiple") {
		t.Error("poll help should describe multiple ID behavior")
	}
	if !strings.Contains(text, "step ID") {
		t.Error("poll help should mention step ID")
	}
	if !strings.Contains(text, "attempt ID") {
		t.Error("poll help should mention attempt ID")
	}
}
