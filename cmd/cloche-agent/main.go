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
		fmt.Fprintf(os.Stderr, "usage: cloche-agent <workflow-file> [--resume-from <step>] [--start-step <step>]\n")
		os.Exit(1)
	}

	workflowPath := os.Args[1]
	workDir, _ := os.Getwd()

	// Parse --resume-from and --start-step flags
	var resumeFromStep, startStep string
	for i := 2; i < len(os.Args); i++ {
		if os.Args[i] == "--resume-from" && i+1 < len(os.Args) {
			i++
			resumeFromStep = os.Args[i]
		} else if os.Args[i] == "--start-step" && i+1 < len(os.Args) {
			i++
			startStep = os.Args[i]
		}
	}

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
	taskID := os.Getenv("CLOCHE_TASK_ID")
	os.Unsetenv("CLOCHE_TASK_ID")
	attemptID := os.Getenv("CLOCHE_ATTEMPT_ID")
	os.Unsetenv("CLOCHE_ATTEMPT_ID")

	runner := agent.NewRunner(agent.RunnerConfig{
		WorkflowPath:   workflowPath,
		WorkDir:        workDir,
		StatusOutput:   os.Stdout,
		RunID:          runID,
		TaskID:         taskID,
		AttemptID:      attemptID,
		ResumeFromStep: resumeFromStep,
		StartStep:      startStep,
	})

	if err := runner.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
