package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/projectcli"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func cmdProject(args []string) {
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
	}
	if err := projectCommand(args, addr, os.Stdout); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

// projectCommand executes the project command, writing output to w.
// Extracted from cmdProject so BDD tests can call it without os.Exit.
func projectCommand(args []string, addr string, w io.Writer) error {
	// Check for subcommands first.
	if len(args) >= 2 && args[0] == "repos" && args[1] == "list" {
		return projectReposListCommand(args[2:], addr, w)
	}

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

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.GetProjectInfoRequest{}
	if name != "" {
		req.Name = name
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		req.ProjectDir = cwd
	}

	resp, err := client.GetProjectInfo(ctx, req)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	printProjectInfo(resp, w)
	return nil
}

// projectReposListCommand implements "cloche project repos list".
func projectReposListCommand(args []string, addr string, w io.Writer) error {
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

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req := &pb.GetProjectInfoRequest{}
	if name != "" {
		req.Name = name
	} else {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		req.ProjectDir = cwd
	}

	resp, err := client.GetProjectInfo(ctx, req)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	projectcli.WriteReposList(resp.Repositories, w)
	return nil
}

func printProjectInfo(resp *pb.GetProjectInfoResponse, w io.Writer) {
	fmt.Fprintf(w, "Project:     %s\n", resp.Name)
	fmt.Fprintf(w, "Directory:   %s\n", resp.ProjectDir)
	fmt.Fprintln(w)

	fmt.Fprintln(w, "Config:")
	fmt.Fprintf(w, "  active:             %v\n", resp.Active)
	fmt.Fprintf(w, "  concurrency:        %d\n", resp.Concurrency)
	if resp.StaggerSeconds > 0 {
		fmt.Fprintf(w, "  stagger_seconds:    %.1f\n", resp.StaggerSeconds)
	}
	if resp.DedupSeconds > 0 {
		fmt.Fprintf(w, "  dedup_seconds:      %.0f\n", resp.DedupSeconds)
	}
	fmt.Fprintf(w, "  stop_on_error:      %v\n", resp.StopOnError)
	fmt.Fprintf(w, "  max_consecutive_failures: %d\n", resp.MaxConsecutiveFailures)
	fmt.Fprintf(w, "  evolution:          %v\n", resp.EvolutionEnabled)
	fmt.Fprintln(w)

	loopState := "stopped"
	if resp.LoopRunning {
		loopState = "running"
	}
	fmt.Fprintf(w, "Loop:        %s\n", loopState)
	fmt.Fprintln(w)

	if len(resp.ActiveRuns) > 0 {
		fmt.Fprintf(w, "Active runs: %d\n", len(resp.ActiveRuns))
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
			fmt.Fprintln(w, line)
		}
	} else {
		fmt.Fprintln(w, "Active runs: none")
	}
	fmt.Fprintln(w)

	if len(resp.ContainerWorkflows) > 0 {
		fmt.Fprintln(w, "Container workflows:")
		for _, wfName := range resp.ContainerWorkflows {
			fmt.Fprintf(w, "  %s\n", wfName)
		}
	} else {
		fmt.Fprintln(w, "Container workflows: none")
	}

	if len(resp.HostWorkflows) > 0 {
		fmt.Fprintln(w, "Host workflows:")
		for _, wfName := range resp.HostWorkflows {
			fmt.Fprintf(w, "  %s\n", wfName)
		}
	} else {
		fmt.Fprintln(w, "Host workflows: none")
	}

	if len(resp.Repositories) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "Repositories:")
		for _, repo := range resp.Repositories {
			def := ""
			if repo.Default {
				def = "  (default)"
			}
			if repo.Url != "" {
				fmt.Fprintf(w, "  %-20s  %-30s  %s%s\n", repo.Name, repo.Path, repo.Url, def)
			} else {
				fmt.Fprintf(w, "  %-20s  %s%s\n", repo.Name, repo.Path, def)
			}
		}
	}
}
