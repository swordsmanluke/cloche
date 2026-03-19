package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/runcontext"
	"github.com/cloche-dev/cloche/internal/version"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

func main() {
	if len(os.Args) < 2 {
		printTopLevelHelp()
		os.Exit(1)
	}

	// Handle version flags before anything else
	if os.Args[1] == "-v" || os.Args[1] == "--version" {
		cmdVersion()
		return
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
	case "workflow":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("workflow")
			return
		}
		cmdWorkflow(os.Args[2:])
		return
	case "project":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("project")
			return
		}
		cmdProject(os.Args[2:])
		return
	case "validate":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("validate")
			return
		}
		cmdValidate(os.Args[2:])
		return
	case "complete":
		// No help flag handling: complete must be fast and quiet.
		cmdComplete(os.Args[2:])
		return
	}

	// Handle --help for daemon commands before connecting
	daemonCmds := map[string]bool{
		"run": true, "resume": true, "status": true, "logs": true, "poll": true,
		"list": true, "stop": true, "delete": true, "loop": true, "shutdown": true,
	}
	if daemonCmds[os.Args[1]] && hasHelpFlag(os.Args[2:]) {
		printSubcommandHelp(os.Args[1])
		return
	}

	// Commands that need a daemon connection
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultSocketAddr()
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
	case "resume":
		cmdResume(ctx, client, os.Args[2:])
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
		cmdShutdown(ctx, client, os.Args[2:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", os.Args[1])
		printTopLevelHelp()
		os.Exit(1)
	}
}


func cmdRun(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	var workflowSpec, prompt, title, issueID string
	var keepContainer bool

	for i := 0; i < len(args); i++ {
		switch args[i] {
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
		case "--issue", "-i":
			if i+1 < len(args) {
				i++
				issueID = args[i]
			}
		case "--keep-container":
			keepContainer = true
		default:
			if workflowSpec == "" && !strings.HasPrefix(args[i], "-") {
				workflowSpec = args[i]
			}
		}
	}

	if workflowSpec == "" {
		fmt.Fprintf(os.Stderr, "usage: cloche run <workflow>[:<step>] [--prompt \"...\"] [--title \"...\"] [--issue ID]\n")
		os.Exit(1)
	}

	// workflowSpec is "workflow" or "workflow:step"; extract the workflow name for image lookup.
	workflowName, _, _ := strings.Cut(workflowSpec, ":")

	cwd, _ := os.Getwd()

	// Resolve image from workflow file (soft failure — fall back to daemon default).
	// Try loading from any .cloche file; only extract image for container workflows.
	var image string
	if wf, err := loadWorkflow(cwd, workflowName); err == nil && wf.Location == domain.LocationContainer {
		image = wf.Config["container.image"]
	}

	resp, err := client.RunWorkflow(ctx, &pb.RunWorkflowRequest{
		WorkflowName:  workflowSpec,
		ProjectDir:    cwd,
		Image:         image,
		Prompt:        prompt,
		KeepContainer: keepContainer,
		Title:         title,
		IssueId:       issueID,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Started run: %s\n", resp.RunId)
	if resp.TaskId != "" {
		fmt.Printf("Task:        %s\n", resp.TaskId)
	}
	if resp.AttemptId != "" {
		fmt.Printf("Attempt:     %s\n", resp.AttemptId)
	}
}

func cmdResume(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche resume <run-id> [step-name]\n")
		os.Exit(1)
	}

	runID := args[0]
	stepName := ""
	if len(args) > 1 {
		stepName = args[1]
	}

	// Send resume via gRPC metadata on a RunWorkflow call
	md := metadata.Pairs(
		"x-cloche-resume-run-id", runID,
		"x-cloche-resume-step", stepName,
	)
	ctx = metadata.NewOutgoingContext(ctx, md)

	resp, err := client.RunWorkflow(ctx, &pb.RunWorkflowRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Resumed run: %s\n", resp.RunId)
}

func cmdStatus(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	// Parse flags to detect --all before checking positional args.
	var all bool
	var positional []string
	for _, arg := range args {
		if arg == "--all" {
			all = true
		} else {
			positional = append(positional, arg)
		}
	}

	if len(positional) > 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche status [<task-id>]\n")
		os.Exit(1)
	}

	// If a task ID is provided, show the latest attempt status for that task.
	if len(positional) == 1 {
		cmdStatusTaskLatest(ctx, client, positional[0])
		return
	}

	// No ID: show daemon status overview.
	cmdStatusOverview(ctx, client, os.Stdout, all)
}

// cmdStatusTaskLatest fetches the task and displays its latest attempt status.
func cmdStatusTaskLatest(ctx context.Context, client pb.ClocheServiceClient, taskID string) {
	resp, err := client.GetTask(ctx, &pb.GetTaskRequest{TaskId: taskID})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Task:    %s\n", resp.TaskId)
	if resp.Title != "" {
		fmt.Printf("Title:   %s\n", resp.Title)
	}
	fmt.Printf("Status:  %s\n", resp.Status)
	if resp.ProjectDir != "" {
		fmt.Printf("Project: %s\n", resp.ProjectDir)
	}

	if len(resp.Attempts) == 0 {
		fmt.Println("Attempt: none")
		return
	}

	latest := resp.Attempts[len(resp.Attempts)-1]
	fmt.Printf("Attempt: %s\n", latest.AttemptId)
	fmt.Printf("Result:  %s\n", latest.Result)
	if latest.EndedAt != "" && latest.EndedAt != "0001-01-01 00:00:00 +0000 UTC" {
		fmt.Printf("Ended:   %s\n", latest.EndedAt)
	}
}

func cmdStatusOverview(ctx context.Context, client pb.ClocheServiceClient, w io.Writer, all bool) {
	// Get daemon version.
	verResp, err := client.GetVersion(ctx, &pb.GetVersionRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(w, "Daemon version: %s\n", verResp.Version)

	cwd, _ := os.Getwd()
	_, hasClocheDir := os.Stat(filepath.Join(cwd, ".cloche"))

	if !all && hasClocheDir == nil {
		// In a project directory: show project-specific info.
		cmdStatusProject(ctx, client, w, cwd)
	} else {
		// Not in a project directory or --all: show global overview.
		cmdStatusGlobal(ctx, client, w)
	}
}

func cmdStatusProject(ctx context.Context, client pb.ClocheServiceClient, w io.Writer, projectDir string) {
	info, err := client.GetProjectInfo(ctx, &pb.GetProjectInfoRequest{ProjectDir: projectDir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Fprintf(w, "Project: %s\n", info.Name)
	fmt.Fprintf(w, "Concurrency: %d\n", info.Concurrency)

	loopStatus := "stopped"
	if info.LoopRunning {
		if info.ErrorHalted {
			loopStatus = "halted"
		} else {
			loopStatus = "running"
		}
	}
	fmt.Fprintf(w, "Orchestration loop: %s\n", loopStatus)

	// Fetch runs for the past hour to compute success rate.
	listResp, err := client.ListRuns(ctx, &pb.ListRunsRequest{ProjectDir: projectDir})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var succeeded int
	for _, run := range listResp.Runs {
		if run.State == "succeeded" {
			succeeded++
		}
	}
	fmt.Fprintf(w, "Runs (past hour): %d / %d succeeded\n", succeeded, len(listResp.Runs))

	// Active runs from project info.
	fmt.Fprintf(w, "Active runs: %d\n", len(info.ActiveRuns))
	for _, run := range info.ActiveRuns {
		dur := formatDuration(run.StartedAt)
		fmt.Fprintf(w, "  %s: %s\n", run.RunId, dur)
	}
}

func cmdStatusGlobal(ctx context.Context, client pb.ClocheServiceClient, w io.Writer) {
	// Server filters to past hour when All is not set.
	listResp, err := client.ListRuns(ctx, &pb.ListRunsRequest{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	var succeeded, activeCount int
	type activeRun struct {
		id        string
		startedAt string
	}
	var actives []activeRun

	for _, run := range listResp.Runs {
		if run.State == "succeeded" {
			succeeded++
		}
		if run.State == "running" || run.State == "pending" {
			activeCount++
			actives = append(actives, activeRun{id: run.RunId, startedAt: run.StartedAt})
		}
	}

	fmt.Fprintf(w, "Runs (past hour): %d / %d succeeded\n", succeeded, len(listResp.Runs))
	fmt.Fprintf(w, "Active runs: %d\n", activeCount)
	for _, a := range actives {
		dur := formatDuration(a.startedAt)
		fmt.Fprintf(w, "  %s: %s\n", a.id, dur)
	}
}

// formatDuration parses a Go time string and returns a human-readable duration since then.
func formatDuration(startedAt string) string {
	parsed, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", startedAt)
	if err != nil {
		return startedAt
	}
	d := time.Since(parsed)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
}

func cmdList(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	var all bool
	var projectDir, stateFilter string
	var limit int32
	var runs bool // --runs flag to show flat run listing instead of tasks

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--all":
			all = true
		case "--runs":
			runs = true
		case "--project", "-p":
			if i+1 < len(args) {
				i++
				projectDir = args[i]
			}
		case "--state", "-s":
			if i+1 < len(args) {
				i++
				stateFilter = args[i]
			}
		case "--limit", "-n":
			if i+1 < len(args) {
				i++
				n, err := strconv.Atoi(args[i])
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: invalid --limit value: %s\n", args[i])
					os.Exit(1)
				}
				limit = int32(n)
			}
		}
	}

	if runs {
		cmdListRuns(ctx, client, all, projectDir, stateFilter, limit)
		return
	}

	// Default: task-oriented listing
	req := &pb.ListTasksRequest{
		State: stateFilter,
		Limit: limit,
	}
	if projectDir != "" {
		req.ProjectDir = projectDir
	} else if all {
		req.All = true
	} else {
		cwd, _ := os.Getwd()
		req.ProjectDir = cwd
	}

	resp, err := client.ListTasks(ctx, req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(resp.Tasks) == 0 {
		fmt.Println("No tasks found.")
		return
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "TASK ID\tSTATUS\tATTEMPTS\tLATEST ATTEMPT\tTITLE")
	for _, task := range resp.Tasks {
		title := task.Title
		if len(title) > 50 {
			title = title[:47] + "..."
		}
		latestAttempt := task.LatestAttemptId
		if latestAttempt == "" {
			latestAttempt = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			task.TaskId, task.Status, task.AttemptCount, latestAttempt, title)
	}
	w.Flush()
}

// cmdListRuns shows a flat run listing (legacy mode, accessible via --runs).
func cmdListRuns(ctx context.Context, client pb.ClocheServiceClient, all bool, projectDir, stateFilter string, limit int32) {
	req := &pb.ListRunsRequest{
		State: stateFilter,
		Limit: limit,
	}
	if projectDir != "" {
		req.ProjectDir = projectDir
	} else if all {
		req.All = true
	} else {
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

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUN ID\tWORKFLOW\tSTATE\tTYPE\tTASK ID\tTITLE\tERROR")
	for _, run := range resp.Runs {
		runType := "container"
		if run.IsHost {
			runType = "host"
		}
		title := run.Title
		if len(title) > 40 {
			title = title[:37] + "..."
		}
		errMsg := run.ErrorMessage
		if len(errMsg) > 60 {
			errMsg = errMsg[:57] + "..."
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			run.RunId, run.WorkflowName, run.State, runType,
			run.TaskId, title, errMsg)
	}
	w.Flush()
}

func cmdLogs(client pb.ClocheServiceClient, args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche logs <id> [--type <full|script|llm>] [-f] [-l <n>]\n")
		fmt.Fprintf(os.Stderr, "  <id>: task ID, attempt ID (a133), workflow ID (a133:develop), or step ID (a133:develop:review)\n")
		os.Exit(1)
	}

	var stepFilter, typeFilter string
	var follow bool
	var limit int
	id := args[0]

	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--step", "-s":
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
		case "--limit", "-l":
			if i+1 < len(args) {
				i++
				n, err := strconv.Atoi(args[i])
				if err != nil || n < 0 {
					fmt.Fprintf(os.Stderr, "error: --limit requires a positive integer\n")
					os.Exit(1)
				}
				limit = n
			}
		}
	}

	// Use background context — log output can be large and follow mode blocks.
	ctx := context.Background()
	var mdPairs []string
	if follow {
		mdPairs = append(mdPairs, "x-cloche-follow", "true")
	}
	if limit > 0 {
		mdPairs = append(mdPairs, "x-cloche-limit", strconv.Itoa(limit))
	}
	if len(mdPairs) > 0 {
		md := metadata.Pairs(mdPairs...)
		ctx = metadata.NewOutgoingContext(ctx, md)
	}

	// Pass id via the Id field so the server can resolve task IDs, attempt IDs,
	// run IDs, and composite IDs (task:attempt:step).
	stream, err := client.StreamLogs(ctx, &pb.StreamLogsRequest{
		Id:       id,
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

	// Check for "resume" subcommand
	if len(args) > 0 && args[0] == "resume" {
		_, err := client.ResumeLoop(ctx, &pb.ResumeLoopRequest{ProjectDir: cwd})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Orchestration loop resumed.")
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

func cmdShutdown(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	var force bool
	for _, a := range args {
		if a == "-f" || a == "--force" {
			force = true
		}
	}
	_, err := client.Shutdown(ctx, &pb.ShutdownRequest{Force: force})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Daemon shutting down.")
}

// resolveRunContext returns the project directory and task ID for context
// commands. The task ID comes from CLOCHE_TASK_ID and the project directory
// from CLOCHE_PROJECT_DIR (falling back to cwd).
func resolveRunContext() (projectDir, taskID string, err error) {
	taskID = os.Getenv("CLOCHE_TASK_ID")
	if taskID == "" {
		return "", "", fmt.Errorf("CLOCHE_TASK_ID environment variable is not set")
	}
	projectDir = os.Getenv("CLOCHE_PROJECT_DIR")
	if projectDir == "" {
		projectDir, err = os.Getwd()
		if err != nil {
			return "", "", fmt.Errorf("getting working directory: %w", err)
		}
	}
	return projectDir, taskID, nil
}

func cmdGet(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche get <key>\n")
		os.Exit(1)
	}

	projectDir, taskID, err := resolveRunContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	val, ok, err := runcontext.Get(projectDir, taskID, args[0])
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

	projectDir, taskID, err := resolveRunContext()
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

	if err := runcontext.Set(projectDir, taskID, args[0], value); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func cmdVersion() {
	cliVersion := version.Version()
	fmt.Printf("cloche %s\n", cliVersion)

	// Try to get daemon version via gRPC
	daemonVersion := "<unavailable>"
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultSocketAddr()
	}
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err == nil {
		defer conn.Close()
		client := pb.NewClocheServiceClient(conn)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		resp, err := client.GetVersion(ctx, &pb.GetVersionRequest{})
		if err == nil {
			daemonVersion = resp.Version
		}
	}
	fmt.Printf("cloched %s\n", daemonVersion)

	// Try to get agent version by running: docker run --rm <image> cloche-agent -v
	agentVersion := "<unavailable>"
	image := os.Getenv("CLOCHE_IMAGE")
	if image == "" {
		image = "cloche-agent:latest"
	}
	out, err := exec.CommandContext(
		context.Background(),
		"docker", "run", "--rm", "--entrypoint", "cloche-agent", image, "-v",
	).Output()
	if err == nil {
		agentVersion = strings.TrimSpace(string(out))
		// Strip "cloche-agent " prefix if present
		agentVersion = strings.TrimPrefix(agentVersion, "cloche-agent ")
	}
	fmt.Printf("cloche-agent %s\n", agentVersion)

	// Warn about version mismatches
	if daemonVersion != "<unavailable>" && daemonVersion != cliVersion {
		fmt.Fprintf(os.Stderr, "warning: CLI version (%s) differs from daemon version (%s)\n", cliVersion, daemonVersion)
	}
	if agentVersion != "<unavailable>" && agentVersion != cliVersion {
		fmt.Fprintf(os.Stderr, "warning: CLI version (%s) differs from agent version (%s)\n", cliVersion, agentVersion)
	}
}
