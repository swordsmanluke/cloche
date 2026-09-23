package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/swordsmanluke/cloche/api/clochepb"
	"github.com/swordsmanluke/cloche/internal/config"
	"github.com/swordsmanluke/cloche/internal/projectcli"
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
	if len(args) >= 1 && args[0] == "purge" {
		return projectPurgeCommand(args[1:], addr, w, os.Stdin)
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
			if repo.Url != "" {
				fmt.Fprintf(w, "  %-20s  %-30s  %s\n", repo.Name, repo.Path, repo.Url)
			} else {
				fmt.Fprintf(w, "  %-20s  %s\n", repo.Name, repo.Path)
			}
		}
	} else {
		fmt.Fprintln(w)
		fmt.Fprintf(w, "DEPRECATED: No repository configuration found in .cloche/config.toml.\n")
		fmt.Fprintf(w, "  To configure repositories, add a [[repositories]] section:\n")
		fmt.Fprintf(w, "    [[repositories]]\n")
		fmt.Fprintf(w, "    name = \"main\"\n")
		fmt.Fprintf(w, "    path = \".\"\n")
	}
}

// projectPurgeCommand implements "cloche project purge": it deletes every
// record the daemon holds for a project so it disappears from `cloche list`
// and the console. The daemon refuses while the loop or any run is active;
// this side only adds the confirmation, since the deletion is not reversible.
func projectPurgeCommand(args []string, addr string, w io.Writer, in io.Reader) error {
	var name, dir string
	yes := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--name":
			if i+1 < len(args) {
				i++
				name = args[i]
			}
		case "--yes", "-y":
			yes = true
		default:
			if strings.HasPrefix(args[i], "-") {
				return fmt.Errorf("unknown flag %q", args[i])
			}
			dir = args[i]
		}
	}
	if name == "" && dir == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getting working directory: %w", err)
		}
		dir = cwd
	}
	if dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolving %s: %w", dir, err)
		}
		dir = abs
	}

	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()
	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Look the project up first so the confirmation names what will go and
	// a typo fails before anything is asked.
	info, err := client.GetProjectInfo(ctx, &pb.GetProjectInfoRequest{ProjectDir: dir, Name: name})
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	if !yes {
		fmt.Fprintf(w, "This permanently deletes all runs, tasks, attempts and logs metadata for %s (%s) from the daemon.\n", info.Name, info.ProjectDir)
		fmt.Fprintf(w, "Files under %s/.cloche/ are not touched. Continue? [y/N] ", info.ProjectDir)
		var answer string
		fmt.Fscanln(in, &answer)
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(w, "aborted")
			return nil
		}
	}

	resp, err := client.PurgeProject(ctx, &pb.PurgeProjectRequest{ProjectDir: info.ProjectDir})
	if err != nil {
		return fmt.Errorf("%w", err)
	}
	fmt.Fprintf(w, "Purged project %s (%s): %d runs deleted\n", resp.Name, resp.ProjectDir, resp.RunsDeleted)
	return nil
}
