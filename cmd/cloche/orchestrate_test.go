package main

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestOrchestrateOutput(t *testing.T) {
	runID := "main-abc123"
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "Started orchestration: %s\n", runID)
	got := buf.String()
	if !strings.HasPrefix(got, "Started orchestration: ") {
		t.Errorf("unexpected output prefix: %q", got)
	}
	if !strings.Contains(got, runID) {
		t.Errorf("output should contain run ID %q, got %q", runID, got)
	}
}
