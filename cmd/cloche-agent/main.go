package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloche-dev/cloche/internal/agent"
	"github.com/cloche-dev/cloche/internal/version"
)

func main() {
	if len(os.Args) >= 2 && (os.Args[1] == "-v" || os.Args[1] == "--version") {
		fmt.Printf("cloche-agent %s\n", version.Version())
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		fmt.Fprintf(os.Stderr, "error: CLOCHE_ADDR not set\n")
		os.Exit(1)
	}

	runID := os.Getenv("CLOCHE_RUN_ID")
	os.Unsetenv("CLOCHE_RUN_ID")
	taskID := os.Getenv("CLOCHE_TASK_ID")
	os.Unsetenv("CLOCHE_TASK_ID")
	attemptID := os.Getenv("CLOCHE_ATTEMPT_ID")
	os.Unsetenv("CLOCHE_ATTEMPT_ID")

	workDir, _ := os.Getwd()

	sess := agent.NewSession(agent.SessionConfig{
		Addr:      addr,
		RunID:     runID,
		AttemptID: attemptID,
		TaskID:    taskID,
		WorkDir:   workDir,
	})

	if err := sess.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
