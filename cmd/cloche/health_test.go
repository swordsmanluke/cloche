package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"google.golang.org/grpc"
)

func TestColorStatus_NoTTY(t *testing.T) {
	// In tests, stdout is not a TTY, so colorStatus should return plain text.
	tests := []struct {
		input string
		want  string
	}{
		{"green", "green"},
		{"yellow", "yellow"},
		{"red", "red"},
		{"grey", "grey"},
		{"blue", "blue"},
	}
	for _, tt := range tests {
		got := colorStatus(tt.input)
		if got != tt.want {
			t.Errorf("colorStatus(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// versionOnlyServer implements just GetVersion, enough to stand in for the
// daemon's gRPC listener in tests of reportWebUnreachable.
type versionOnlyServer struct {
	pb.UnimplementedClocheServiceServer
	resp *pb.GetVersionResponse
}

func (s *versionOnlyServer) GetVersion(context.Context, *pb.GetVersionRequest) (*pb.GetVersionResponse, error) {
	return s.resp, nil
}

// captureStderr redirects os.Stderr for the duration of fn and returns what
// was written to it.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = orig

	var buf bytes.Buffer
	buf.ReadFrom(r)
	return buf.String()
}

// TestReportWebUnreachable_DaemonUp_WebDown verifies that when the web
// dashboard HTTP call fails but the daemon's gRPC listener is reachable and
// reports its web dashboard as down, `cloche health` explains the situation
// instead of printing a bare "connection refused" that looks like the whole
// daemon crashed.
func TestReportWebUnreachable_DaemonUp_WebDown(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	grpcServer := grpc.NewServer()
	pb.RegisterClocheServiceServer(grpcServer, &versionOnlyServer{resp: &pb.GetVersionResponse{
		Version:  "3.20.8",
		WebAddr:  "0.0.0.0:8080",
		WebUp:    false,
		WebError: "bind: address already in use",
	}})
	go grpcServer.Serve(lis)
	defer grpcServer.Stop()

	origAddr := os.Getenv("CLOCHE_ADDR")
	os.Setenv("CLOCHE_ADDR", lis.Addr().String())
	defer os.Setenv("CLOCHE_ADDR", origAddr)

	out := captureStderr(t, func() {
		reportWebUnreachable(errors.New("dial tcp 127.0.0.1:8080: connect: connection refused"))
	})

	if !strings.Contains(out, "web dashboard is down") {
		t.Errorf("expected explanation of down web dashboard, got:\n%s", out)
	}
	if !strings.Contains(out, "bind: address already in use") {
		t.Errorf("expected bind error detail, got:\n%s", out)
	}
	if !strings.Contains(out, "3.20.8") {
		t.Errorf("expected daemon version, got:\n%s", out)
	}
}

// TestReportWebUnreachable_DaemonFullyDown verifies that when gRPC is also
// unreachable (the whole daemon is down, not just its web dashboard),
// reportWebUnreachable falls back to the original HTTP error instead of
// claiming a partial outage it can't confirm.
func TestReportWebUnreachable_DaemonFullyDown(t *testing.T) {
	// Reserve a port and close it immediately so nothing is listening.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	addr := lis.Addr().String()
	lis.Close()

	origAddr := os.Getenv("CLOCHE_ADDR")
	os.Setenv("CLOCHE_ADDR", addr)
	defer os.Setenv("CLOCHE_ADDR", origAddr)

	httpErr := errors.New("dial tcp 127.0.0.1:8080: connect: connection refused")
	out := captureStderr(t, func() {
		reportWebUnreachable(httpErr)
	})

	if !strings.Contains(out, httpErr.Error()) {
		t.Errorf("expected original HTTP error to be reported when daemon is fully down, got:\n%s", out)
	}
	if strings.Contains(out, "web dashboard is down") {
		t.Errorf("should not claim a partial outage when gRPC is also unreachable, got:\n%s", out)
	}
}
