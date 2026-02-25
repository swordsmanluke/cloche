package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloche-dev/cloche/internal/agent"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: cloche-agent <workflow-file>\n")
		os.Exit(1)
	}

	workflowPath := os.Args[1]
	workDir, _ := os.Getwd()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		cancel()
	}()

	// Read and clear push config env vars so test subprocesses
	// (go test ./...) don't inherit them and accidentally push.
	runID := os.Getenv("CLOCHE_RUN_ID")
	gitRemote := os.Getenv("CLOCHE_GIT_REMOTE")
	os.Unsetenv("CLOCHE_RUN_ID")
	os.Unsetenv("CLOCHE_GIT_REMOTE")

	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      workDir,
		StatusOutput: os.Stdout,
		RunID:        runID,
		GitRemote:    gitRemote,
	})

	if err := runner.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
