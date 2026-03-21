package main

import (
	"os"
	"strings"

	"golang.org/x/term"
)

// noColorFlag is set true when --no-color flag is present or NO_COLOR env var is set.
var noColorFlag bool

const (
	ansiReset  = "\033[0m"
	ansiBold   = "\033[1m"
	ansiCyan   = "\033[36m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
)

// isTTY returns true if stdout is connected to a terminal.
func isTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// colorEnabled returns true when ANSI color output should be used.
// Color is disabled if --no-color was passed or NO_COLOR env var is set (per
// https://no-color.org/). Color is forced on if CLOCHE_FORCE_COLOR is set.
// Otherwise color is enabled only when stdout is a terminal.
func colorEnabled() bool {
	if noColorFlag {
		return false
	}
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if os.Getenv("CLOCHE_FORCE_COLOR") != "" {
		return true
	}
	return isTTY()
}

// colorStatus wraps a status/state string in ANSI color codes.
// Green: succeeded, running, green
// Red: failed, red
// Yellow: pending, cancelled, halted, yellow
func colorStatus(s string) string {
	if !colorEnabled() {
		return s
	}
	switch strings.ToLower(s) {
	case "succeeded", "running", "green":
		return ansiGreen + s + ansiReset
	case "failed", "red":
		return ansiRed + s + ansiReset
	case "pending", "cancelled", "halted", "yellow":
		return ansiYellow + s + ansiReset
	}
	return s
}

// colorID wraps an ID string in bold cyan.
func colorID(s string) string {
	if !colorEnabled() {
		return s
	}
	return ansiBold + ansiCyan + s + ansiReset
}
