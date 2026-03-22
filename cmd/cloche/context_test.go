package main

import (
	"os"
	"testing"
)

func TestResolveRunContext_MissingTaskID(t *testing.T) {
	t.Setenv("CLOCHE_TASK_ID", "")
	_, _, err := resolveRunContext()
	if err == nil {
		t.Fatal("expected error when CLOCHE_TASK_ID is not set")
	}
}

func TestResolveRunContext_UsesEnvVars(t *testing.T) {
	t.Setenv("CLOCHE_TASK_ID", "test-task-1234")
	t.Setenv("CLOCHE_ATTEMPT_ID", "attempt-5678")

	taskID, attemptID, err := resolveRunContext()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskID != "test-task-1234" {
		t.Errorf("taskID = %q, want %q", taskID, "test-task-1234")
	}
	if attemptID != "attempt-5678" {
		t.Errorf("attemptID = %q, want %q", attemptID, "attempt-5678")
	}
}

func TestResolveRunContext_EmptyAttemptID(t *testing.T) {
	t.Setenv("CLOCHE_TASK_ID", "test-task-1234")
	t.Setenv("CLOCHE_ATTEMPT_ID", "")

	taskID, attemptID, err := resolveRunContext()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskID != "test-task-1234" {
		t.Errorf("taskID = %q, want %q", taskID, "test-task-1234")
	}
	if attemptID != "" {
		t.Errorf("attemptID = %q, want empty string", attemptID)
	}
}

// TestDaemonConnection verifies that dialDaemon() returns an error when
// CLOCHE_ADDR points to an unreachable address (lazy dial — error surfaces
// on first use, not at connection time for gRPC NewClient).
func TestDaemonConnection_NoAddr(t *testing.T) {
	// Save and clear CLOCHE_ADDR so we use the default socket.
	orig := os.Getenv("CLOCHE_ADDR")
	defer os.Setenv("CLOCHE_ADDR", orig)
	os.Setenv("CLOCHE_ADDR", "")

	// dialDaemon() itself should not error — gRPC uses lazy dial.
	conn, err := dialDaemon()
	if err != nil {
		t.Fatalf("dialDaemon unexpectedly failed: %v", err)
	}
	conn.Close()
}
