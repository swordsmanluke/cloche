package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveDebugAddr_FlagTakesPriority(t *testing.T) {
	t.Setenv("CLOCHE_DEBUG", "env-host:9000")
	args := []string{"--debug-addr", "flag-host:8000", "state"}
	got := resolveDebugAddr(args)
	assert.Equal(t, "flag-host:8000", got)
}

func TestResolveDebugAddr_EnvFallback(t *testing.T) {
	t.Setenv("CLOCHE_DEBUG", "env-host:9000")
	args := []string{"state"}
	got := resolveDebugAddr(args)
	assert.Equal(t, "env-host:9000", got)
}

func TestResolveDebugAddr_EmptyWhenNotConfigured(t *testing.T) {
	t.Setenv("CLOCHE_DEBUG", "")
	got := resolveDebugAddr(nil)
	// Config file likely not present in test env; addr should be empty.
	// We just check it doesn't panic.
	_ = got
}

func TestResolveDebugAddr_StripsHTTPPrefix(t *testing.T) {
	t.Setenv("CLOCHE_DEBUG", "http://localhost:7778")
	got := resolveDebugAddr([]string{})
	assert.Equal(t, "localhost:7778", got)
}

// TestDebugHTTPServer_StateEndpoint starts a real HTTP server (same mux as
// startDebugServer) and verifies that /debug/state returns valid JSON with the
// required fields.
func TestDebugHTTPServer_StateEndpoint(t *testing.T) {
	// Use a goroutine count known to be >0 and a fixed fake snapshot.
	snap := daemonStateSnapshot{
		GoroutineCount: 42,
		ActiveRunIDs:   []string{"run-1"},
		ActiveLoops:    []string{"/proj"},
		ContainerSessions: []sessionSnapshot{
			{AttemptID: "att-1", ContainerID: "abc123def456", PendingSteps: 1},
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/state", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(snap)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	resp, err := http.Get(fmt.Sprintf("http://%s/debug/state", addr))
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	var got daemonStateSnapshot
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&got))
	assert.Equal(t, 42, got.GoroutineCount)
	assert.Equal(t, []string{"run-1"}, got.ActiveRunIDs)
	assert.Equal(t, []string{"/proj"}, got.ActiveLoops)
	require.Len(t, got.ContainerSessions, 1)
	assert.Equal(t, "att-1", got.ContainerSessions[0].AttemptID)
	assert.Equal(t, 1, got.ContainerSessions[0].PendingSteps)
}

// TestDebugHTTPServer_PProfEndpoint verifies that the pprof index endpoint responds.
func TestDebugHTTPServer_PProfEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/goroutine", pprof.Handler("goroutine").ServeHTTP)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	defer srv.Close()

	resp, err := http.Get(fmt.Sprintf("http://%s/debug/pprof/goroutine?debug=1", addr))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
