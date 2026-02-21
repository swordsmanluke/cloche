package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}

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
	case "list":
		cmdList(ctx, client)
	case "stop":
		cmdStop(ctx, client, os.Args[2:])
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: cloche <command> [args]

Commands:
  run --workflow <name> [--prompt "..."]  Launch a workflow run
  status <run-id>                         Check run status
  list                                    List all runs
  stop <run-id>                           Stop a running workflow
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
	resp, err := client.RunWorkflow(ctx, &pb.RunWorkflowRequest{
		WorkflowName: workflow,
		ProjectDir:   cwd,
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
	fmt.Printf("Step:     %s\n", resp.CurrentStep)
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
		fmt.Printf("%s  %-20s  %s  %s\n", run.RunId, run.WorkflowName, run.State, run.StartedAt)
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
