package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/dsl"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/runcontext"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	if len(os.Args) < 2 {
		printTopLevelHelp()
		os.Exit(1)
	}

	// Handle top-level help: "cloche help", "cloche --help", "cloche -h"
	switch os.Args[1] {
	case "help":
		printHelp(os.Args[2:])
		return
	case "--help", "-h":
		printTopLevelHelp()
		return
	}

	// Commands that don't need a daemon connection
	switch os.Args[1] {
	case "init":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("init")
			return
		}
		cmdInit(os.Args[2:])
		return
	case "health":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("health")
			return
		}
		cmdHealth(os.Args[2:])
		return
	case "get":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("get")
			return
		}
		cmdGet(os.Args[2:])
		return
	case "set":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("set")
			return
		}
		cmdSet(os.Args[2:])
		return
	case "tasks":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("tasks")
			return
		}
		cmdTasks(os.Args[2:])
		return
	}

	// Handle --help for daemon commands before connecting
	daemonCmds := map[string]bool{
		"run": true, "status": true, "logs": true, "poll": true,
		"list": true, "stop": true, "delete": true, "loop": true, "shutdown": true,
	}
	if daemonCmds[os.Args[1]] && hasHelpFlag(os.Args[2:]) {
		printSubcommandHelp(os.Args[1])
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
	case "poll":
		cmdPoll(client, os.Args[2:])
	case "list":
		cmdList(ctx, client, os.Args[2:])
	case "stop":
		cmdStop(ctx, client, os.Args[2:])
	case "delete":
		cmdDelete(ctx, client, os.Args[2:])
	case "loop":
		cmdLoop(ctx, client, os.Args[2:])
	case "shutdown":
		cmdShutdown(ctx, client)
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printTopLevelHelp()
		os.Exit(1)
	}
}


func cmdRun(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	var workflow, prompt, title string
	var keepContainer bool

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
		case "--title":
			if i+1 < len(args) {
				i++
				title = args[i]
			}
		case "--keep-container":
			keepContainer = true
		default:
			// Support bare positional arg as workflow name for backwards compat
			if workflow == "" && !strings.HasPrefix(args[i], "-") {
				workflow = args[i]
			}
		}
	}

	if workflow == "" {
		fmt.Fprintf(os.Stderr, "usage: cloche run --workflow <name> [--prompt \"...\"] [--title \"...\"]\n")
		os.Exit(1)
	}

	cwd, _ := os.Getwd()

	// Resolve image from workflow file (soft failure — fall back to daemon default)
	var image string
	wfPath := filepath.Join(cwd, ".cloche", workflow+".cloche")
	if data, err := os.ReadFile(wfPath); err == nil {
		if wf, err := dsl.ParseForContainer(string(data)); err == nil {
			image = wf.Config["container.image"]
		}
	}

	resp, err := client.RunWorkflow(ctx, &pb.RunWorkflowRequest{
		WorkflowName:  workflow,
		ProjectDir:    cwd,
		Image:         image,
		Prompt:        prompt,
		KeepContainer: keepContainer,
		Title:         title,
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

	fmt.Printf("Run:       %s\n", resp.RunId)
	if resp.Title != "" {
		fmt.Printf("Title:     %s\n", resp.Title)
	}
	fmt.Printf("Workflow:  %s\n", resp.WorkflowName)
	if resp.IsHost {
		fmt.Printf("Type:      host\n")
	} else {
		fmt.Printf("Type:      container\n")
	}
	fmt.Printf("State:     %s\n", resp.State)
	if resp.ContainerId != "" {
		cid := resp.ContainerId
		if len(cid) > 12 {
			cid = cid[:12]
		}
		fmt.Printf("Container: %s\n", cid)
	}
	if resp.ErrorMessage != "" {
		fmt.Printf("Error:     %s\n", resp.ErrorMessage)
	}
	fmt.Printf("Active:    %s\n", resp.CurrentStep)
	for _, exec := range resp.StepExecutions {
		fmt.Printf("  %s: %s (%s -> %s)\n", exec.StepName, exec.Result, exec.StartedAt, exec.CompletedAt)
	}
}

func cmdList(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	var all bool
	for _, arg := range args {
		if arg == "--all" {
			all = true
		}
	}

	req := &pb.ListRunsRequest{}
	if all {
		// --all: show all runs across all projects (no time limit)
		req.All = true
	} else {
		// Default: filter to current project
		cwd, _ := os.Getwd()
		req.ProjectDir = cwd
	}

	resp, err := client.ListRuns(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Runs) == 0 {
		fmt.Println("No runs found.")
		return
	}

	for _, run := range resp.Runs {
		runType := "container"
		if run.IsHost {
			runType = "host"
		}
		line := fmt.Sprintf("%s  %-20s  %-10s  %-9s", run.RunId, run.WorkflowName, run.State, runType)
		if run.Title != "" {
			t := run.Title
			if len(t) > 40 {
				t = t[:37] + "..."
			}
			line += "  " + t
		}
		if run.ContainerId != "" {
			line += "  " + run.ContainerId
		}
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
		fmt.Fprintf(os.Stderr, "usage: cloche logs <run-id> [--step <name>] [--type <full|script|llm>] [-f]\n")
		os.Exit(1)
	}

	var stepFilter, typeFilter string
	var follow bool
	runID := args[0]

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--step":
			if i+1 < len(args) {
				i++
				stepFilter = args[i]
			}
		case "--type":
			if i+1 < len(args) {
				i++
				typeFilter = args[i]
			}
		case "--follow", "-f":
			follow = true
		}
	}

	// Use background context — log output can be large and follow mode blocks.
	ctx := context.Background()
	if follow {
		md := metadata.Pairs("x-cloche-follow", "true")
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	stream, err := client.StreamLogs(ctx, &pb.StreamLogsRequest{
		RunId:    runID,
		StepName: stepFilter,
		LogType:  typeFilter,
	})
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
		case "full_log":
			fmt.Print(string(logstream.ParseClaudeStream([]byte(entry.Message))))
		case "log":
			// Live-streamed log line from an active run.
			fmt.Println(entry.Message)
		default:
			// Handles filtered log entries like "script_log", "llm_log", "step_log"
			if entry.StepName != "" {
				fmt.Printf("--- %s ---\n", entry.StepName)
			}
			if entry.Message != "" {
				fmt.Print(string(logstream.ParseClaudeStream([]byte(entry.Message))))
			}
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

func cmdDelete(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche delete <container-or-run-id>\n")
		os.Exit(1)
	}

	_, err := client.DeleteContainer(ctx, &pb.DeleteContainerRequest{Id: args[0]})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Deleted container: %s\n", args[0])
}

func cmdLoop(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	cwd, _ := os.Getwd()

	// Check for "stop" subcommand
	if len(args) > 0 && args[0] == "stop" {
		_, err := client.DisableLoop(ctx, &pb.DisableLoopRequest{ProjectDir: cwd})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Orchestration loop stopped.")
		return
	}

	// Default: 0 means "use config value" (server reads .cloche/config.toml).
	var maxConcurrent int32
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--max":
			if i+1 < len(args) {
				i++
				n, err := fmt.Sscanf(args[i], "%d", &maxConcurrent)
				if n != 1 || err != nil {
					fmt.Fprintf(os.Stderr, "invalid --max value: %s\n", args[i])
					os.Exit(1)
				}
			}
		}
	}

	_, err := client.EnableLoop(ctx, &pb.EnableLoopRequest{
		ProjectDir:    cwd,
		MaxConcurrent: maxConcurrent,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if maxConcurrent > 0 {
		fmt.Printf("Orchestration loop started (max_concurrent=%d).\n", maxConcurrent)
	} else {
		fmt.Println("Orchestration loop started (using config defaults).")
	}
}

func cmdTasks(args []string) {
	// Determine HTTP address from env
	httpAddr := os.Getenv("CLOCHE_HTTP")
	if httpAddr == "" {
		httpAddr = "localhost:8080"
	}
	httpAddr = strings.TrimPrefix(httpAddr, "http://")

	// Determine project label from --project flag or current directory
	projectDir := ""
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--project":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		}
	}

	if projectDir == "" {
		cwd, _ := os.Getwd()
		projectDir = filepath.Base(cwd)
	}

	tasksURL := fmt.Sprintf("http://%s/api/projects/%s/tasks", httpAddr, projectDir)
	resp, err := http.Get(tasksURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error connecting to daemon web API: %v\n", err)
		fmt.Fprintf(os.Stderr, "hint: ensure CLOCHE_HTTP is set and the daemon is running with --http\n")
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: HTTP %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		os.Exit(1)
	}

	type taskEntry struct {
		ID          string `json:"id"`
		Status      string `json:"status"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Assigned    bool   `json:"assigned"`
		AssignedAt  string `json:"assigned_at"`
		RunID       string `json:"run_id"`
	}

	var tasks []taskEntry
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		fmt.Fprintf(os.Stderr, "error parsing response: %v\n", err)
		os.Exit(1)
	}

	if len(tasks) == 0 {
		fmt.Println("No tasks found. (Is the orchestration loop running?)")
		return
	}

	fmt.Printf("%-20s  %-12s  %-10s  %-30s  %s\n", "ID", "STATUS", "ASSIGNED", "RUN", "TITLE")
	for _, t := range tasks {
		status := t.Status
		if status == "" {
			status = "open"
		}
		assigned := "-"
		if t.Assigned {
			assigned = "yes"
		}
		runID := ""
		if t.RunID != "" {
			runID = t.RunID
			if len(runID) > 30 {
				runID = runID[:27] + "..."
			}
		}
		title := t.Title
		if len(title) > 40 {
			title = title[:37] + "..."
		}
		fmt.Printf("%-20s  %-12s  %-10s  %-30s  %s\n", t.ID, status, assigned, runID, title)
	}
}

func cmdShutdown(ctx context.Context, client pb.ClocheServiceClient) {
	_, err := client.Shutdown(ctx, &pb.ShutdownRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Daemon shutting down.")
}

// resolveRunContext returns the project directory and run ID for context
// commands. The run ID comes from CLOCHE_RUN_ID and the project directory
// from CLOCHE_PROJECT_DIR (falling back to cwd).
func resolveRunContext() (projectDir, runID string, err error) {
	runID = os.Getenv("CLOCHE_RUN_ID")
	if runID == "" {
		return "", "", fmt.Errorf("CLOCHE_RUN_ID environment variable is not set")
	}
	projectDir = os.Getenv("CLOCHE_PROJECT_DIR")
	if projectDir == "" {
		projectDir, err = os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("getting working directory: %w", err)
		}
	}
	return projectDir, runID, nil
}

func cmdGet(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche get <key>\n")
		os.Exit(1)
	}

	projectDir, runID, err := resolveRunContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	val, ok, err := runcontext.Get(projectDir, runID, args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !ok {
		os.Exit(1)
	}
	fmt.Println(val)
}

func cmdSet(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: cloche set <key> <value>\n")
		fmt.Fprintf(os.Stderr, "       cloche set <key> -     (read value from stdin)\n")
		os.Exit(1)
	}

	projectDir, runID, err := resolveRunContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	value := args[1]
	if value == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
			os.Exit(1)
		}
		value = strings.TrimRight(string(data), "\n")
	}

	if err := runcontext.Set(projectDir, runID, args[0], value); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
