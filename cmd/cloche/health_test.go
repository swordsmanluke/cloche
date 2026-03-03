package main

import (
	"testing"
)

func TestColorStatus_NoTTY(t *testing.T) {
	// In tests, stdout is not a TTY, so colorStatus should return plain text.
	tests := []struct {
		input string
		want  string
	}{
		{"green", "green"},
		{"yellow", "yellow"},
		{"red", "red"},
		{"grey", "grey"},
		{"blue", "blue"},
	}
	for _, tt := range tests {
		got := colorStatus(tt.input)
		if got != tt.want {
			t.Errorf("colorStatus(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
