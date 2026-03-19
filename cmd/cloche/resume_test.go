package main

import (
	"testing"
)

func TestParseResumeArg_WorkflowID(t *testing.T) {
	taskOrRun, runID, stepName, err := parseResumeArg("a133:develop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	if runID != "a133" {
		t.Errorf("expected runID %q, got %q", "a133", runID)
	}
	if stepName != "" {
		t.Errorf("expected empty stepName, got %q", stepName)
	}
}

func TestParseResumeArg_StepID(t *testing.T) {
	taskOrRun, runID, stepName, err := parseResumeArg("a133:develop:review")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	if runID != "a133" {
		t.Errorf("expected runID %q, got %q", "a133", runID)
	}
	if stepName != "review" {
		t.Errorf("expected stepName %q, got %q", "review", stepName)
	}
}

func TestParseResumeArg_LongRunID(t *testing.T) {
	taskOrRun, runID, stepName, err := parseResumeArg("develop-lush-fern-470c:develop")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	if runID != "develop-lush-fern-470c" {
		t.Errorf("expected runID %q, got %q", "develop-lush-fern-470c", runID)
	}
	if stepName != "" {
		t.Errorf("expected empty stepName, got %q", stepName)
	}
}

func TestParseResumeArg_LongRunIDWithStep(t *testing.T) {
	taskOrRun, runID, stepName, err := parseResumeArg("develop-lush-fern-470c:develop:implement")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	if runID != "develop-lush-fern-470c" {
		t.Errorf("expected runID %q, got %q", "develop-lush-fern-470c", runID)
	}
	if stepName != "implement" {
		t.Errorf("expected stepName %q, got %q", "implement", stepName)
	}
}

func TestParseResumeArg_TaskID(t *testing.T) {
	taskOrRun, runID, stepName, err := parseResumeArg("cloche-k4gh")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "cloche-k4gh" {
		t.Errorf("expected taskOrRunID %q, got %q", "cloche-k4gh", taskOrRun)
	}
	if runID != "" {
		t.Errorf("expected empty runID, got %q", runID)
	}
	if stepName != "" {
		t.Errorf("expected empty stepName, got %q", stepName)
	}
}

func TestParseResumeArg_BareRunID(t *testing.T) {
	taskOrRun, runID, stepName, err := parseResumeArg("pqpm-main")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "pqpm-main" {
		t.Errorf("expected taskOrRunID %q, got %q", "pqpm-main", taskOrRun)
	}
	if runID != "" {
		t.Errorf("expected empty runID, got %q", runID)
	}
	if stepName != "" {
		t.Errorf("expected empty stepName, got %q", stepName)
	}
}

func TestParseResumeArg_EmptyRunID(t *testing.T) {
	_, _, _, err := parseResumeArg(":develop")
	if err == nil {
		t.Fatal("expected error for empty run ID")
	}
}

func TestParseResumeArg_Empty(t *testing.T) {
	_, _, _, err := parseResumeArg("")
	if err == nil {
		t.Fatal("expected error for empty argument")
	}
}

func TestParseResumeArg_StepNameWithColon(t *testing.T) {
	// Extra colons beyond the third segment are ignored (SplitN limit=3)
	taskOrRun, runID, stepName, err := parseResumeArg("a133:develop:step:extra")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if taskOrRun != "" {
		t.Errorf("expected empty taskOrRunID, got %q", taskOrRun)
	}
	if runID != "a133" {
		t.Errorf("expected runID %q, got %q", "a133", runID)
	}
	// SplitN with n=3 puts "step:extra" in parts[2]
	if stepName != "step:extra" {
		t.Errorf("expected stepName %q, got %q", "step:extra", stepName)
	}
}
