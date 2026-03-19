package main

import (
	"strings"
	"testing"
)

func TestHasHelpFlag(t *testing.T) {
	tests := []struct {
		args []string
		want bool
	}{
		{nil, false},
		{[]string{}, false},
		{[]string{"develop"}, false},
		{[]string{"--help"}, true},
		{[]string{"-h"}, true},
		{[]string{"develop", "--help"}, true},
		{[]string{"-h", "develop"}, true},
	}

	for _, tt := range tests {
		got := hasHelpFlag(tt.args)
		if got != tt.want {
			t.Errorf("hasHelpFlag(%v) = %v, want %v", tt.args, got, tt.want)
		}
	}
}

func TestSubcommandHelpExists(t *testing.T) {
	commands := []string{
		"init", "health", "run", "resume", "status", "logs", "poll",
		"list", "stop", "delete", "tasks", "loop", "get", "set", "shutdown",
		"workflow", "project",
	}

	for _, cmd := range commands {
		text, ok := subcommandHelp[cmd]
		if !ok {
			t.Errorf("missing help text for subcommand %q", cmd)
			continue
		}
		if text == "" {
			t.Errorf("empty help text for subcommand %q", cmd)
		}
	}
}

func TestSubcommandHelpContainsUsage(t *testing.T) {
	for cmd, text := range subcommandHelp {
		if !strings.Contains(text, "Usage:") {
			t.Errorf("help for %q missing Usage: section", cmd)
		}
		if !strings.Contains(text, "Examples:") {
			t.Errorf("help for %q missing Examples: section", cmd)
		}
		if !strings.Contains(text, "cloche "+cmd) {
			t.Errorf("help for %q missing usage line with 'cloche %s'", cmd, cmd)
		}
	}
}

func TestPrintHelp_UnknownCommand(t *testing.T) {
	// printHelp with an unknown command should still return true (help was handled)
	got := printHelp([]string{"nonexistent"})
	if !got {
		t.Error("printHelp with unknown command should return true")
	}
}

func TestPrintHelp_NoArgs(t *testing.T) {
	got := printHelp(nil)
	if !got {
		t.Error("printHelp with no args should return true")
	}
}

func TestPrintHelp_ValidCommand(t *testing.T) {
	got := printHelp([]string{"run"})
	if !got {
		t.Error("printHelp with valid command should return true")
	}
}

func TestRunHelpIncludesIssueFlag(t *testing.T) {
	text := subcommandHelp["run"]
	if !strings.Contains(text, "--issue") {
		t.Error("run help text should document --issue flag")
	}
	if !strings.Contains(text, "-i") {
		t.Error("run help text should document -i shorthand")
	}
}

func TestRunHelpPositionalArg(t *testing.T) {
	text := subcommandHelp["run"]
	// Should document positional workflow arg, not --workflow flag
	if strings.Contains(text, "--workflow") {
		t.Error("run help text should not reference removed --workflow flag")
	}
	// Should document workflow:step format
	if !strings.Contains(text, "<workflow>:<step>") {
		t.Error("run help text should document workflow:step format")
	}
	// Should include example of step-specific run
	if !strings.Contains(text, "develop:review") {
		t.Error("run help text should include example of workflow:step usage")
	}
}

// TestParseWorkflowSpec verifies the workflow:step parsing logic used in cmdRun.
func TestParseWorkflowSpec(t *testing.T) {
	tests := []struct {
		spec         string
		wantWorkflow string
		wantStep     string
	}{
		{"develop", "develop", ""},
		{"develop:review", "develop", "review"},
		{"build:test", "build", "test"},
	}
	for _, tt := range tests {
		workflow, step, _ := strings.Cut(tt.spec, ":")
		if workflow != tt.wantWorkflow {
			t.Errorf("Cut(%q) workflow = %q, want %q", tt.spec, workflow, tt.wantWorkflow)
		}
		if step != tt.wantStep {
			t.Errorf("Cut(%q) step = %q, want %q", tt.spec, step, tt.wantStep)
		}
	}
}

func TestListHelpIncludesTaskIdColumn(t *testing.T) {
	text := subcommandHelp["list"]
	if !strings.Contains(text, "task ID") {
		t.Error("list help text should mention task ID column")
	}
}
