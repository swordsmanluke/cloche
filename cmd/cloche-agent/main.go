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

	runID := os.Getenv("CLOCHE_RUN_ID")
	os.Unsetenv("CLOCHE_RUN_ID")

	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath: workflowPath,
		WorkDir:      workDir,
		StatusOutput: os.Stdout,
		RunID:        runID,
	})

	if err := runner.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
