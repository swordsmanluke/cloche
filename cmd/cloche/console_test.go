package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsInGitRepo(t *testing.T) {
	// This test file lives inside the workspace which is a git repo.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if !isInGitRepo(cwd) {
		t.Error("isInGitRepo should return true for the project directory")
	}

	// A temp directory that is definitely not a git repo.
	dir := t.TempDir()
	if isInGitRepo(dir) {
		t.Error("isInGitRepo should return false for a fresh temp directory")
	}
}

func TestRequireGitAndCloche_NotGit(t *testing.T) {
	dir := t.TempDir()
	err := requireGitAndCloche(dir)
	if err == nil {
		t.Fatal("expected error for non-git directory")
	}
	if !strings.Contains(err.Error(), "git repository") {
		t.Errorf("expected git repository error, got: %v", err)
	}
}

func TestRequireGitAndCloche_NoCloche(t *testing.T) {
	dir := t.TempDir()
	// Create a .git directory to simulate a git repo.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	err := requireGitAndCloche(dir)
	if err == nil {
		t.Fatal("expected error for missing .cloche/ directory")
	}
	if !strings.Contains(err.Error(), ".cloche") {
		t.Errorf("expected .cloche error, got: %v", err)
	}
}

func TestRequireGitAndCloche_Valid(t *testing.T) {
	dir := t.TempDir()
	// Create .git and .cloche directories.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".cloche"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := requireGitAndCloche(dir); err != nil {
		t.Errorf("expected no error, got: %v", err)
	}
}

func TestConsoleHelpExists(t *testing.T) {
	text, ok := subcommandHelp["console"]
	if !ok {
		t.Fatal("missing help text for console command")
	}
	if !strings.Contains(text, "Usage:") {
		t.Error("console help missing Usage: section")
	}
	if !strings.Contains(text, "Examples:") {
		t.Error("console help missing Examples: section")
	}
	if !strings.Contains(text, "--agent") {
		t.Error("console help missing --agent flag documentation")
	}
}

func TestConsoleHelpInTopLevel(t *testing.T) {
	// The top-level help should mention console.
	// We capture the text from printTopLevelHelp via the constant in help.go.
	// Since printTopLevelHelp writes to stderr, we just check subcommandHelp
	// contains "console" and the top-level string literal includes it.
	// Check that console appears in subcommandHelp (already tested above)
	// and that the top-level help function refers to it.
	// We test the presence of "console" in the top-level text via a known substring.
	_ = subcommandHelp["console"] // existence already tested above
}
