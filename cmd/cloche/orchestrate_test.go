package main

import (
	"bytes"
	"fmt"
	"testing"
)

func TestOrchestrateOutput(t *testing.T) {
	tests := []struct {
		dispatched int32
		want       string
	}{
		{0, "No ready work found.\n"},
		{1, "Dispatched 1 run(s).\n"},
		{5, "Dispatched 5 run(s).\n"},
	}
	for _, tt := range tests {
		var buf bytes.Buffer
		if tt.dispatched == 0 {
			fmt.Fprintln(&buf, "No ready work found.")
		} else {
			fmt.Fprintf(&buf, "Dispatched %d run(s).\n", tt.dispatched)
		}
		if buf.String() != tt.want {
			t.Errorf("dispatched=%d: got %q, want %q", tt.dispatched, buf.String(), tt.want)
		}
	}
}
