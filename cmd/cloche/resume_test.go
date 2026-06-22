package main

import (
	"testing"
)

func TestParseResumeArg_WorkflowID(t *testing.T) {
	taskOrRun, compositeID, err := parseResumeArg("a133:develop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	// Full composite ID is preserved so server can resolve via resolveRunIDFromID.
	if compositeID != "a133:develop" {
		t.Errorf("expected compositeID %q, got %q", "a133:develop", compositeID)
	}
}

func TestParseResumeArg_StepID(t *testing.T) {
	taskOrRun, compositeID, err := parseResumeArg("a133:develop:review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	// Full composite ID (including step) is preserved for server-side resolution.
	if compositeID != "a133:develop:review" {
		t.Errorf("expected compositeID %q, got %q", "a133:develop:review", compositeID)
	}
}

func TestParseResumeArg_TaskAttemptWorkflow(t *testing.T) {
	// Canonical 3-part Workflow ID: task:attempt:workflow
	taskOrRun, compositeID, err := parseResumeArg("TASK-123:a41k:develop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	if compositeID != "TASK-123:a41k:develop" {
		t.Errorf("expected compositeID %q, got %q", "TASK-123:a41k:develop", compositeID)
	}
}

func TestParseResumeArg_LongRunID(t *testing.T) {
	taskOrRun, compositeID, err := parseResumeArg("develop-lush-fern-470c:develop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	if compositeID != "develop-lush-fern-470c:develop" {
		t.Errorf("expected compositeID %q, got %q", "develop-lush-fern-470c:develop", compositeID)
	}
}

func TestParseResumeArg_LongRunIDWithStep(t *testing.T) {
	taskOrRun, compositeID, err := parseResumeArg("develop-lush-fern-470c:develop:implement")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	if compositeID != "develop-lush-fern-470c:develop:implement" {
		t.Errorf("expected compositeID %q, got %q", "develop-lush-fern-470c:develop:implement", compositeID)
	}
}

func TestParseResumeArg_TaskID(t *testing.T) {
	taskOrRun, compositeID, err := parseResumeArg("cloche-k4gh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "cloche-k4gh" {
		t.Errorf("expected taskOrRunID %q, got %q", "cloche-k4gh", taskOrRun)
	}
	if compositeID != "" {
		t.Errorf("expected empty compositeID, got %q", compositeID)
	}
}

func TestParseResumeArg_BareRunID(t *testing.T) {
	taskOrRun, compositeID, err := parseResumeArg("pqpm-main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "pqpm-main" {
		t.Errorf("expected taskOrRunID %q, got %q", "pqpm-main", taskOrRun)
	}
	if compositeID != "" {
		t.Errorf("expected empty compositeID, got %q", compositeID)
	}
}

func TestParseResumeArg_EmptyRunID(t *testing.T) {
	_, _, err := parseResumeArg(":develop")
	if err == nil {
		t.Fatal("expected error for empty first component")
	}
}

func TestParseResumeArg_Empty(t *testing.T) {
	_, _, err := parseResumeArg("")
	if err == nil {
		t.Fatal("expected error for empty argument")
	}
}

func TestParseResumeArg_StepNameWithColon(t *testing.T) {
	// Extra colons are preserved in the composite ID for server-side handling.
	taskOrRun, compositeID, err := parseResumeArg("a133:develop:step:extra")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	if compositeID != "a133:develop:step:extra" {
		t.Errorf("expected compositeID %q, got %q", "a133:develop:step:extra", compositeID)
	}
}

func TestParseResumeFlags_Default(t *testing.T) {
	mode, pos, err := parseResumeFlags([]string{"TASK-123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "rebuild" {
		t.Errorf("expected mode %q, got %q", "rebuild", mode)
	}
	if len(pos) != 1 || pos[0] != "TASK-123" {
		t.Errorf("expected positional [TASK-123], got %v", pos)
	}
}

func TestParseResumeFlags_NoRebuild(t *testing.T) {
	mode, pos, err := parseResumeFlags([]string{"--no-rebuild", "TASK-123"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "reuse" {
		t.Errorf("expected mode %q, got %q", "reuse", mode)
	}
	if len(pos) != 1 || pos[0] != "TASK-123" {
		t.Errorf("expected positional [TASK-123], got %v", pos)
	}
}

func TestParseResumeFlags_Clean(t *testing.T) {
	mode, pos, err := parseResumeFlags([]string{"TASK-123", "--clean"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mode != "clean" {
		t.Errorf("expected mode %q, got %q", "clean", mode)
	}
	if len(pos) != 1 || pos[0] != "TASK-123" {
		t.Errorf("expected positional [TASK-123], got %v", pos)
	}
}

func TestParseResumeFlags_BothMutuallyExclusive(t *testing.T) {
	_, _, err := parseResumeFlags([]string{"--no-rebuild", "--clean", "TASK-123"})
	if err == nil {
		t.Fatal("expected error when both --no-rebuild and --clean are set")
	}
}
