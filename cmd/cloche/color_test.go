package main

import (
	"os"
	"testing"
)

func TestColorStatus_NoColor(t *testing.T) {
	// colorEnabled() returns false in test env (stdout is not a TTY).
	tests := []struct {
		input string
		want  string
	}{
		{"succeeded", "succeeded"},
		{"running", "running"},
		{"failed", "failed"},
		{"pending", "pending"},
		{"cancelled", "cancelled"},
		{"halted", "halted"},
		{"unknown", "unknown"},
		{"", ""},
	}
	for _, tt := range tests {
		got := colorStatus(tt.input)
		if got != tt.want {
			t.Errorf("colorStatus(%q) = %q, want %q (no-color mode)", tt.input, got, tt.want)
		}
	}
}

func TestColorID_NoColor(t *testing.T) {
	// colorEnabled() returns false in test env (stdout is not a TTY).
	got := colorID("TASK-42")
	if got != "TASK-42" {
		t.Errorf("colorID(%q) = %q, want plain string in no-color mode", "TASK-42", got)
	}
}

func TestColorEnabled_NoColorFlag(t *testing.T) {
	orig := noColorFlag
	defer func() { noColorFlag = orig }()

	noColorFlag = true
	if colorEnabled() {
		t.Error("colorEnabled() should return false when noColorFlag is set")
	}
}

func TestColorEnabled_NoColorEnv(t *testing.T) {
	orig := noColorFlag
	defer func() { noColorFlag = orig }()
	noColorFlag = false

	os.Setenv("NO_COLOR", "1")
	defer os.Unsetenv("NO_COLOR")

	if colorEnabled() {
		t.Error("colorEnabled() should return false when NO_COLOR env var is set")
	}
}
