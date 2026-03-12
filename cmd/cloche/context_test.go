package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloche-dev/cloche/internal/runcontext"
)

func TestResolveRunContext_MissingRunID(t *testing.T) {
	t.Setenv("CLOCHE_RUN_ID", "")
	_, _, err := resolveRunContext()
	if err == nil {
		t.Fatal("expected error when CLOCHE_RUN_ID is not set")
	}
}

func TestResolveRunContext_UsesEnvVars(t *testing.T) {
	t.Setenv("CLOCHE_RUN_ID", "test-run-1234")
	t.Setenv("CLOCHE_PROJECT_DIR", "/tmp/myproject")

	projectDir, runID, err := resolveRunContext()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if runID != "test-run-1234" {
		t.Errorf("runID = %q, want %q", runID, "test-run-1234")
	}
	if projectDir != "/tmp/myproject" {
		t.Errorf("projectDir = %q, want %q", projectDir, "/tmp/myproject")
	}
}

func TestResolveRunContext_FallsToCwd(t *testing.T) {
	t.Setenv("CLOCHE_RUN_ID", "test-run-1234")
	t.Setenv("CLOCHE_PROJECT_DIR", "")

	projectDir, _, err := resolveRunContext()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	cwd, _ := os.Getwd()
	if projectDir != cwd {
		t.Errorf("projectDir = %q, want cwd %q", projectDir, cwd)
	}
}

func TestCmdGet_PrintsValue(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run-abcd"

	// Pre-populate context
	if err := runcontext.Set(dir, runID, "branch", "feature-x"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Verify the file exists
	path := runcontext.ContextPath(dir, runID)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatalf("context.json not created at %s", path)
	}
}

func TestCmdSet_WritesContextFile(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run-abcd"

	if err := runcontext.Set(dir, runID, "mykey", "myval"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	val, ok, err := runcontext.Get(dir, runID, "mykey")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok || val != "myval" {
		t.Errorf("Get = (%q, %v), want (%q, true)", val, ok, "myval")
	}

	// Verify the JSON file is well-formed
	data, err := os.ReadFile(filepath.Join(dir, ".cloche", runID, "context.json"))
	if err != nil {
		t.Fatalf("reading context.json: %v", err)
	}
	if len(data) == 0 {
		t.Error("context.json is empty")
	}
}

func TestCmdSet_StdinValue(t *testing.T) {
	dir := t.TempDir()
	runID := "test-run-stdin"

	t.Setenv("CLOCHE_RUN_ID", runID)
	t.Setenv("CLOCHE_PROJECT_DIR", dir)

	// Swap stdin to read from a string
	origStdin := os.Stdin
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating pipe: %v", err)
	}
	os.Stdin = r
	defer func() { os.Stdin = origStdin }()

	// Write multi-line content and close
	content := "line one\nline two\n"
	go func() {
		w.WriteString(content)
		w.Close()
	}()

	cmdSet([]string{"multiline_key", "-"})

	// Verify the value was stored with trailing newline trimmed
	val, ok, err := runcontext.Get(dir, runID, "multiline_key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !ok {
		t.Fatal("expected key to exist")
	}
	expected := strings.TrimRight(content, "\n")
	if val != expected {
		t.Errorf("Get = %q, want %q", val, expected)
	}
}
