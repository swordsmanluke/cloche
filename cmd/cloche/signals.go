package main

import (
	"fmt"
	"io"
	"os"

	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/signal"
)

// cmdSignals dispatches "cloche signals <subcommand>". There is deliberately
// no command for sending a signal: scripts use curl against the endpoint.
func cmdSignals(args []string) {
	if err := signalsCommand(args, signal.NewTokenStore(config.SignalTokenDir()), os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

const signalsUsage = "usage: cloche signals token [--rotate] [--project <dir>]"

// signalsCommand implements the signals subcommands against tokens, writing
// to w. Split from cmdSignals so tests can call it without os.Exit.
func signalsCommand(args []string, tokens *signal.TokenStore, w io.Writer) error {
	if len(args) == 0 || args[0] != "token" {
		return fmt.Errorf("%s", signalsUsage)
	}
	var project string
	rotate := false
	rest := args[1:]
	for i := 0; i < len(rest); i++ {
		switch rest[i] {
		case "--rotate":
			rotate = true
		case "--project", "-p":
			if i+1 >= len(rest) {
				return fmt.Errorf("--project needs a directory")
			}
			i++
			project = rest[i]
		default:
			return fmt.Errorf("unknown argument %q\n%s", rest[i], signalsUsage)
		}
	}
	dir := resolveProjectDir(project)

	var (
		tok string
		err error
	)
	if rotate {
		tok, err = tokens.Rotate(dir)
	} else {
		tok, err = tokens.Get(dir)
	}
	if err != nil {
		return err
	}
	fmt.Fprintln(w, tok)
	if rotate {
		fmt.Fprintln(os.Stderr, "token rotated: the previous token no longer works")
	}
	return nil
}
