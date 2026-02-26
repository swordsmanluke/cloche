package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/dsl"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

	// Commands that don't need a daemon connection
	switch os.Args[1] {
	case "init":
		cmdInit(os.Args[2:])
		return
	}

	// Commands that need a daemon connection
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = "unix:///tmp/cloche.sock"
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to connect: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	switch os.Args[1] {
	case "run":
		cmdRun(ctx, client, os.Args[2:])
	case "status":
		cmdStatus(ctx, client, os.Args[2:])
	case "logs":
		cmdLogs(client, os.Args[2:])
	case "list":
		cmdList(ctx, client)
	case "stop":
		cmdStop(ctx, client, os.Args[2:])
	case "shutdown":
		cmdShutdown(ctx, client)
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: cloche <command> [args]

Commands:
  init [--workflow <name>] [--image <base>]  Initialize a Cloche project
  run --workflow <name> [--prompt "..."]     Launch a workflow run
  status <run-id>                            Check run status
  logs <run-id>                              Show step logs for a run
  list                                       List all runs
  stop <run-id>                              Stop a running workflow
  shutdown                                   Shut down the daemon
`)
}

func cmdRun(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	var workflow, prompt string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workflow":
			if i+1 < len(args) {
				i++
				workflow = args[i]
			}
		case "--prompt", "-p":
			if i+1 < len(args) {
				i++
				prompt = args[i]
			}
		default:
			// Support bare positional arg as workflow name for backwards compat
			if workflow == "" && !strings.HasPrefix(args[i], "-") {
				workflow = args[i]
			}
		}
	}

	if workflow == "" {
		fmt.Fprintf(os.Stderr, "usage: cloche run --workflow <name> [--prompt \"...\"]\n")
		os.Exit(1)
	}

	cwd, _ := os.Getwd()

	// Resolve image from workflow file (soft failure — fall back to daemon default)
	var image string
	wfPath := filepath.Join(cwd, workflow+".cloche")
	if data, err := os.ReadFile(wfPath); err == nil {
		if wf, err := dsl.Parse(string(data)); err == nil {
			image = wf.Config["container.image"]
		}
	}

	resp, err := client.RunWorkflow(ctx, &pb.RunWorkflowRequest{
		WorkflowName: workflow,
		ProjectDir:   cwd,
		Image:        image,
		Prompt:       prompt,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Started run: %s\n", resp.RunId)
}

func cmdStatus(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche status <run-id>\n")
		os.Exit(1)
	}

	resp, err := client.GetStatus(ctx, &pb.GetStatusRequest{RunId: args[0]})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Run:      %s\n", resp.RunId)
	fmt.Printf("Workflow: %s\n", resp.WorkflowName)
	fmt.Printf("State:    %s\n", resp.State)
	if resp.ErrorMessage != "" {
		fmt.Printf("Error:    %s\n", resp.ErrorMessage)
	}
	fmt.Printf("Active:   %s\n", resp.CurrentStep)
	for _, exec := range resp.StepExecutions {
		fmt.Printf("  %s: %s (%s -> %s)\n", exec.StepName, exec.Result, exec.StartedAt, exec.CompletedAt)
	}
}

func cmdList(ctx context.Context, client pb.ClocheServiceClient) {
	resp, err := client.ListRuns(ctx, &pb.ListRunsRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Runs) == 0 {
		fmt.Println("No runs found.")
		return
	}

	for _, run := range resp.Runs {
		line := fmt.Sprintf("%s  %-20s  %s  %s", run.RunId, run.WorkflowName, run.State, run.StartedAt)
		if run.ErrorMessage != "" {
			errMsg := run.ErrorMessage
			if len(errMsg) > 60 {
				errMsg = errMsg[:57] + "..."
			}
			line += "  " + errMsg
		}
		fmt.Println(line)
	}
}

func cmdLogs(client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche logs <run-id>\n")
		os.Exit(1)
	}

	// Use background context — log output can be large
	stream, err := client.StreamLogs(context.Background(), &pb.StreamLogsRequest{RunId: args[0]})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	for {
		entry, err := stream.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			fmt.Fprintf(os.Stderr, "error reading logs: %v\n", err)
			os.Exit(1)
		}

		switch entry.Type {
		case "step_started":
			fmt.Printf("--- %s started ---\n", entry.StepName)
			if entry.Message != "" {
				fmt.Printf("%s\n", entry.Message)
			}
		case "step_completed":
			fmt.Printf("--- %s: %s ---\n", entry.StepName, entry.Result)
			if entry.Message != "" {
				fmt.Printf("%s\n", entry.Message)
			}
		case "run_completed":
			fmt.Printf("\nRun result: %s\n", entry.Result)
			if entry.Message != "" {
				fmt.Printf("Error:      %s\n", entry.Message)
			}
		default:
			fmt.Printf("[%s] %s: %s\n", entry.Type, entry.StepName, entry.Message)
		}
	}
}

func cmdStop(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche stop <run-id>\n")
		os.Exit(1)
	}

	_, err := client.StopRun(ctx, &pb.StopRunRequest{RunId: args[0]})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Stopped run: %s\n", args[0])
}

func cmdShutdown(ctx context.Context, client pb.ClocheServiceClient) {
	_, err := client.Shutdown(ctx, &pb.ShutdownRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Daemon shutting down.")
}
