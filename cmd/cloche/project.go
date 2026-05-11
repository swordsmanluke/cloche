package main

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/config"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func cmdProject(args []string) {
	// Route subcommands: "cloche project repos <subcommand>"
	if len(args) > 0 && args[0] == "repos" {
		cmdProjectRepos(args[1:])
		return
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

	// Connect to daemon.
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
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
	fmt.Printf("  stop_on_error:      %v\n", resp.StopOnError)
	fmt.Printf("  max_consecutive_failures: %d\n", resp.MaxConsecutiveFailures)
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

	if len(resp.Repositories) > 0 {
		fmt.Println()
		fmt.Println("Repositories:")
		for _, repo := range resp.Repositories {
			def := ""
			if repo.Default {
				def = "  (default)"
			}
			if repo.Url != "" {
				fmt.Printf("  %-20s  %-30s  %s%s\n", repo.Name, repo.Path, repo.Url, def)
			} else {
				fmt.Printf("  %-20s  %s%s\n", repo.Name, repo.Path, def)
			}
		}
	} else {
		fmt.Println()
		fmt.Println("Warning: no repository configuration found.")
		fmt.Println("  This project uses the legacy single-repository model.")
		fmt.Println("  Add [[repositories]] entries to .cloche/config.toml to configure repositories.")
		fmt.Println("  Example:")
		fmt.Println("    [[repositories]]")
		fmt.Println("    name    = \"default\"")
		fmt.Println("    path    = \".\"")
		fmt.Println("    default = true")
	}
}

// cmdProjectRepos handles "cloche project repos <subcommand>".
func cmdProjectRepos(args []string) {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "list", "":
		cmdProjectReposList(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown repos subcommand %q\n", sub)
		fmt.Fprintln(os.Stderr, "Usage: cloche project repos list")
		os.Exit(1)
	}
}

// cmdProjectReposList implements "cloche project repos list".
// Repositories are read from the daemon (which sources them from config.toml).
func cmdProjectReposList(args []string) {
	var name string
	for i := 0; i < len(args); i++ {
		if args[i] == "--name" && i+1 < len(args) {
			i++
			name = args[i]
		}
	}

	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
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

	if len(resp.Repositories) == 0 {
		fmt.Println("No repositories configured.")
		fmt.Println("Add [[repositories]] entries to .cloche/config.toml to configure repositories.")
		return
	}

	fmt.Printf("%-20s  %-30s  %s\n", "NAME", "PATH", "URL")
	fmt.Printf("%-20s  %-30s  %s\n", "----", "----", "---")
	for _, repo := range resp.Repositories {
		def := ""
		if repo.Default {
			def = "  (default)"
		}
		fmt.Printf("%-20s  %-30s  %s%s\n", repo.Name, repo.Path, repo.Url, def)
	}
}
