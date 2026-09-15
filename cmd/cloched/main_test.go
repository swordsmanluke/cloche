package main

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	adaptgrpc "github.com/swordsmanluke/cloche/internal/adapters/grpc"
)

func TestFilterNestedProjects(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "no nesting",
			input: []string{"/a/proj1", "/a/proj2", "/b/proj3"},
			want:  []string{"/a/proj1", "/a/proj2", "/b/proj3"},
		},
		{
			name:  "child filtered",
			input: []string{"/root/project", "/root/project/vendor/sub"},
			want:  []string{"/root/project"},
		},
		{
			name:  "parent kept when listed after child",
			input: []string{"/root/project/vendor/sub", "/root/project"},
			want:  []string{"/root/project"},
		},
		{
			name:  "multiple nested",
			input: []string{"/root", "/root/a", "/root/b", "/other"},
			want:  []string{"/other", "/root"},
		},
		{
			name:  "prefix boundary respected",
			input: []string{"/root/proj", "/root/proj2"},
			want:  []string{"/root/proj", "/root/proj2"},
		},
		{
			name:  "empty input",
			input: nil,
			want:  nil,
		},
		{
			name:  "single entry",
			input: []string{"/only"},
			want:  []string{"/only"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterNestedProjects(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestProjectIsSubpath(t *testing.T) {
	tests := []struct {
		parent string
		child  string
		want   bool
	}{
		{"/root/proj", "/root/proj/sub", true},
		{"/root/proj", "/root/proj/a/b/c", true},
		{"/root/proj", "/root/proj", false},
		{"/root/proj", "/root/proj2", false},
		{"/root/proj", "/root/other", false},
		{"/root", "/root/proj", true},
		{"/root/proj/sub", "/root/proj", false},
	}

	for _, tc := range tests {
		got := projectIsSubpath(tc.parent, tc.child)
		assert.Equal(t, tc.want, got, "projectIsSubpath(%q, %q)", tc.parent, tc.child)
	}
}

func TestNextBackoff(t *testing.T) {
	assert.Equal(t, 2*time.Second, nextBackoff(time.Second, 30*time.Second))
	assert.Equal(t, 30*time.Second, nextBackoff(20*time.Second, 30*time.Second), "doubling past max caps at max")
	assert.Equal(t, 30*time.Second, nextBackoff(30*time.Second, 30*time.Second), "already at max stays at max")
}

// TestServeWebWithRetry_RecoversFromBindFailure reproduces the incident this
// function fixes: the web listener's port is held by something else at
// startup. It must retry instead of giving up, report the down state via
// SetWebStatus, and pick the dashboard back up once the port frees.
func TestServeWebWithRetry_RecoversFromBindFailure(t *testing.T) {
	// Occupy the port so the first bind attempt fails, like a hung previous
	// daemon still holding it during a restart.
	blocker, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := blocker.Addr().String()

	srv := adaptgrpc.NewClocheServer(nil, nil)
	httpServer := &http.Server{Addr: addr, Handler: http.NewServeMux()}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		serveWebWithRetry(ctx, httpServer, addr, srv, 20*time.Millisecond, 50*time.Millisecond)
		close(done)
	}()

	// Bind fails immediately since the port is occupied; status reflects it.
	require.Eventually(t, func() bool {
		ws := srv.GetWebStatus()
		return ws.Addr == addr && !ws.Up && ws.Error != ""
	}, time.Second, 5*time.Millisecond, "expected down status while port is occupied")

	// Free the port; the retry loop should self-heal without intervention.
	require.NoError(t, blocker.Close())
	require.Eventually(t, func() bool {
		ws := srv.GetWebStatus()
		return ws.Addr == addr && ws.Up
	}, 2*time.Second, 5*time.Millisecond, "expected status to recover once the port freed")

	conn, err := net.DialTimeout("tcp", addr, time.Second)
	require.NoError(t, err, "dashboard should be reachable once bound")
	conn.Close()

	cancel()
	httpServer.Close()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serveWebWithRetry did not return after ctx cancellation")
	}
}
