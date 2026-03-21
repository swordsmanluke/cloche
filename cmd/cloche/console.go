package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"golang.org/x/term"
)

func cmdConsole(client pb.ClocheServiceClient, args []string) {
	var agentCommand string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 < len(args) {
				i++
				agentCommand = args[i]
			}
		default:
			if !strings.HasPrefix(args[i], "-") {
				fmt.Fprintf(os.Stderr, "unexpected argument: %s\n", args[i])
				fmt.Fprintf(os.Stderr, "usage: cloche console [--agent <command>]\n")
				os.Exit(1)
			}
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Validate: must be in a git repo with a .cloche/ directory.
	if err := requireGitAndCloche(cwd); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Detect terminal size.
	cols, rows, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		// Non-fatal: use zero values and let the daemon use defaults.
		rows, cols = 0, 0
	}

	// Open the Console gRPC stream with a background context (no timeout — session can be long-lived).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	stream, err := client.Console(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Send ConsoleStart as the first message.
	if err := stream.Send(&pb.ConsoleInput{
		Payload: &pb.ConsoleInput_Start{
			Start: &pb.ConsoleStart{
				ProjectDir:   cwd,
				AgentCommand: agentCommand,
				Rows:         uint32(rows),
				Cols:         uint32(cols),
			},
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Receive ConsoleStarted.
	firstMsg, err := stream.Recv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	started, ok := firstMsg.Payload.(*pb.ConsoleOutput_Started)
	if !ok {
		fmt.Fprintf(os.Stderr, "error: unexpected first message from daemon\n")
		os.Exit(1)
	}
	containerID := started.Started.GetContainerId()

	// Put terminal into raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: failed to set raw mode: %v\n", err)
		os.Exit(1)
	}
	restoreTerminal := func() {
		term.Restore(int(os.Stdin.Fd()), oldState) //nolint:errcheck
	}
	defer restoreTerminal()

	// exitCode is set when ConsoleExited is received.
	exitCode := 0
	done := make(chan struct{})

	// Goroutine: gRPC→stdout pump.
	go func() {
		defer close(done)
		for {
			msg, err := stream.Recv()
			if err != nil {
				if err != io.EOF {
					// Print error after restoring terminal.
					restoreTerminal()
					fmt.Fprintf(os.Stderr, "\r\nerror: stream closed: %v\n", err)
				}
				return
			}
			switch p := msg.Payload.(type) {
			case *pb.ConsoleOutput_Stdout:
				os.Stdout.Write(p.Stdout) //nolint:errcheck
			case *pb.ConsoleOutput_Exited:
				exitCode = int(p.Exited.GetExitCode())
				return
			}
		}
	}()

	// Goroutine: stdin→gRPC pump.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := os.Stdin.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				sendErr := stream.Send(&pb.ConsoleInput{
					Payload: &pb.ConsoleInput_Stdin{Stdin: data},
				})
				if sendErr != nil {
					return
				}
			}
			if err != nil {
				// Stdin closed (Ctrl-D or EOF): close the send side.
				stream.CloseSend() //nolint:errcheck
				return
			}
		}
	}()

	// Goroutine: SIGWINCH→resize pump.
	sigwinch := make(chan os.Signal, 1)
	signal.Notify(sigwinch, syscall.SIGWINCH)
	go func() {
		for range sigwinch {
			newCols, newRows, err := term.GetSize(int(os.Stdin.Fd()))
			if err != nil {
				continue
			}
			stream.Send(&pb.ConsoleInput{ //nolint:errcheck
				Payload: &pb.ConsoleInput_Resize{
					Resize: &pb.TerminalSize{
						Rows: uint32(newRows),
						Cols: uint32(newCols),
					},
				},
			})
		}
	}()

	// Wait for the output pump to finish (stream ended or ConsoleExited received).
	<-done
	signal.Stop(sigwinch)
	close(sigwinch)

	// Restore terminal before printing the summary.
	restoreTerminal()

	fmt.Fprintf(os.Stderr, "\r\nConsole session ended.\r\nContainer: %s\n", containerID)
	os.Exit(exitCode)
}

// requireGitAndCloche checks that dir is inside a git repository and has a
// .cloche/ subdirectory. Returns a non-nil error if either check fails.
func requireGitAndCloche(dir string) error {
	// Check for .git anywhere in the directory tree.
	if !isInGitRepo(dir) {
		return fmt.Errorf("not in a git repository")
	}
	// Check for .cloche/ in the current directory.
	clocheDir := filepath.Join(dir, ".cloche")
	if _, err := os.Stat(clocheDir); os.IsNotExist(err) {
		return fmt.Errorf("no .cloche/ directory found in %s", dir)
	}
	return nil
}

// isInGitRepo returns true if dir or any of its parents contains a .git directory.
func isInGitRepo(dir string) bool {
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return false
		}
		dir = parent
	}
}
