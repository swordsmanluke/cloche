package main

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func cmdProject(args []string) {
	var name string

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				i++
				name = args[i]
			}
		}
	}

	// Connect to daemon.
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.GetProjectInfoRequest{}
	if name != "" {
		req.Name = name
	} else {
		cwd, _ := os.Getwd()
		req.ProjectDir = cwd
	}

	resp, err := client.GetProjectInfo(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Display project info.
	fmt.Printf("Project:     %s\n", resp.Name)
	fmt.Printf("Directory:   %s\n", resp.ProjectDir)
	fmt.Println()

	// Config settings.
	fmt.Println("Config:")
	fmt.Printf("  active:             %v\n", resp.Active)
	fmt.Printf("  concurrency:        %d\n", resp.Concurrency)
	if resp.StaggerSeconds > 0 {
		fmt.Printf("  stagger_seconds:    %.1f\n", resp.StaggerSeconds)
	}
	if resp.DedupSeconds > 0 {
		fmt.Printf("  dedup_seconds:      %.0f\n", resp.DedupSeconds)
	}
	fmt.Printf("  evolution:          %v\n", resp.EvolutionEnabled)
	fmt.Println()

	// Orchestrator loop state.
	loopState := "stopped"
	if resp.LoopRunning {
		loopState = "running"
	}
	fmt.Printf("Loop:        %s\n", loopState)
	fmt.Println()

	// Active runs.
	if len(resp.ActiveRuns) > 0 {
		fmt.Printf("Active runs: %d\n", len(resp.ActiveRuns))
		for _, run := range resp.ActiveRuns {
			runType := "container"
			if run.IsHost {
				runType = "host"
			}
			line := fmt.Sprintf("  %s  %-20s  %-10s  %s", run.RunId, run.WorkflowName, run.State, runType)
			if run.Title != "" {
				t := run.Title
				if len(t) > 40 {
					t = t[:37] + "..."
				}
				line += "  " + t
			}
			fmt.Println(line)
		}
	} else {
		fmt.Println("Active runs: none")
	}
	fmt.Println()

	// Workflows.
	if len(resp.ContainerWorkflows) > 0 {
		fmt.Println("Container workflows:")
		for _, wfName := range resp.ContainerWorkflows {
			fmt.Printf("  %s\n", wfName)
		}
	} else {
		fmt.Println("Container workflows: none")
	}

	if len(resp.HostWorkflows) > 0 {
		fmt.Println("Host workflows:")
		for _, wfName := range resp.HostWorkflows {
			fmt.Printf("  %s\n", wfName)
		}
	} else {
		fmt.Println("Host workflows: none")
	}
}
