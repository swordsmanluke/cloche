package main

import (
	"os"
	"strings"
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

func TestColorEnabled_ForceColor(t *testing.T) {
	orig := noColorFlag
	defer func() { noColorFlag = orig }()
	noColorFlag = false

	os.Setenv("CLOCHE_FORCE_COLOR", "1")
	defer os.Unsetenv("CLOCHE_FORCE_COLOR")

	if !colorEnabled() {
		t.Error("colorEnabled() should return true when CLOCHE_FORCE_COLOR is set")
	}
}

func TestColorEnabled_ForceColorOverridesNoColor(t *testing.T) {
	orig := noColorFlag
	defer func() { noColorFlag = orig }()
	noColorFlag = false

	// --no-color takes precedence over CLOCHE_FORCE_COLOR.
	noColorFlag = true
	os.Setenv("CLOCHE_FORCE_COLOR", "1")
	defer os.Unsetenv("CLOCHE_FORCE_COLOR")

	if colorEnabled() {
		t.Error("colorEnabled() should return false when noColorFlag is set, even with CLOCHE_FORCE_COLOR")
	}
}

func TestColorStatus_WithColor(t *testing.T) {
	os.Setenv("CLOCHE_FORCE_COLOR", "1")
	defer os.Unsetenv("CLOCHE_FORCE_COLOR")

	tests := []struct {
		input    string
		wantCode string
	}{
		{"succeeded", ansiGreen},
		{"running", ansiGreen},
		{"failed", ansiRed},
		{"pending", ansiYellow},
		{"cancelled", ansiYellow},
		{"halted", ansiYellow},
	}
	for _, tt := range tests {
		got := colorStatus(tt.input)
		if !strings.Contains(got, tt.wantCode) {
			t.Errorf("colorStatus(%q) = %q, want ANSI code %q", tt.input, got, tt.wantCode)
		}
		if !strings.Contains(got, ansiReset) {
			t.Errorf("colorStatus(%q) = %q, missing reset code", tt.input, got)
		}
	}
}

func TestColorID_WithColor(t *testing.T) {
	os.Setenv("CLOCHE_FORCE_COLOR", "1")
	defer os.Unsetenv("CLOCHE_FORCE_COLOR")

	got := colorID("TASK-42")
	if !strings.Contains(got, ansiBold) {
		t.Errorf("colorID(%q) = %q, missing bold code", "TASK-42", got)
	}
	if !strings.Contains(got, ansiCyan) {
		t.Errorf("colorID(%q) = %q, missing cyan code", "TASK-42", got)
	}
	if !strings.Contains(got, "TASK-42") {
		t.Errorf("colorID(%q) = %q, missing original string", "TASK-42", got)
	}
	if !strings.Contains(got, ansiReset) {
		t.Errorf("colorID(%q) = %q, missing reset code", "TASK-42", got)
	}
}
