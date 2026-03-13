package main

import (
	"strings"
	"testing"
)

func TestProjectHelpExists(t *testing.T) {
	text, ok := subcommandHelp["project"]
	if !ok {
		t.Fatal("missing help text for project subcommand")
	}
	if !strings.Contains(text, "Usage:") {
		t.Error("project help missing Usage: section")
	}
	if !strings.Contains(text, "Examples:") {
		t.Error("project help missing Examples: section")
	}
	if !strings.Contains(text, "--name") {
		t.Error("project help should mention --name flag")
	}
}

func TestProjectHelpInTopLevel(t *testing.T) {
	// The subcommandHelp map should have a project entry.
	if _, ok := subcommandHelp["project"]; !ok {
		t.Error("project subcommand missing from help registry")
	}
}
