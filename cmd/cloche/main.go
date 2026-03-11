package main

import (
	"bufio"
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
	case "health":
		cmdHealth(os.Args[2:])
		return
	case "tasks":
		cmdTasks(os.Args[2:])
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
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage: cloche <command> [args]

Commands:
  init [--workflow <name>] [--base-image <image>]
                                             Initialize a Cloche project
  health                                   Show project health summary
  run --workflow <name> [--prompt "..."] [--title "..."] [--keep-container]
                                             Launch a workflow run
  status <run-id>                            Check run status
  logs <run-id> [--step <name>] [--type <full|script|llm>] [--follow]
                                             Show logs for a run
  poll <run-id>                              Wait for a run to finish
  list [--all]                                List runs (last hour by default)
  stop <run-id>                              Stop a running workflow
  delete <container-or-run-id>               Delete a retained container
  tasks [--project <dir>]                     Show task pipeline and assignment state
  loop [--max <n>]                            Start orchestration loop (default max=1)
  loop stop                                  Stop orchestration loop
  shutdown                                   Shut down the daemon
`)
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

	resp, err := client.ListRuns(ctx, &pb.ListRunsRequest{All: all})
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
		fmt.Fprintf(os.Stderr, "usage: cloche logs <run-id> [--step <name>] [--type <full|script|llm>] [--follow]\n")
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

	if follow {
		cmdLogsFollow(runID)
		return
	}

	// Use background context — log output can be large
	stream, err := client.StreamLogs(context.Background(), &pb.StreamLogsRequest{
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

func cmdLogsFollow(runID string) {
	// Determine HTTP address from env
	httpAddr := os.Getenv("CLOCHE_HTTP")
	if httpAddr == "" {
		httpAddr = "localhost:8080"
	}
	// Strip protocol prefix if present
	httpAddr = strings.TrimPrefix(httpAddr, "http://")

	sseURL := fmt.Sprintf("http://%s/api/runs/%s/stream", httpAddr, runID)
	req, err := http.NewRequest("GET", sseURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error connecting to SSE stream: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error: HTTP %d: %s\n", resp.StatusCode, strings.TrimSpace(string(body)))
		os.Exit(1)
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: done") {
			break
		}
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var entry struct {
			Timestamp string `json:"timestamp"`
			Type      string `json:"type"`
			Content   string `json:"content"`
		}
		if err := json.Unmarshal([]byte(data), &entry); err != nil {
			continue
		}

		// Color-code by type
		switch entry.Type {
		case "status":
			fmt.Printf("\033[34m[%s] [%s] %s\033[0m\n", entry.Timestamp, entry.Type, entry.Content)
		case "llm":
			fmt.Printf("\033[32m[%s] [%s] %s\033[0m\n", entry.Timestamp, entry.Type, entry.Content)
		default:
			fmt.Printf("[%s] [%s] %s\n", entry.Timestamp, entry.Type, entry.Content)
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
