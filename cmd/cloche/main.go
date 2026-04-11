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
	"syscall"
	"text/tabwriter"
	"time"

	pb "github.com/cloche-dev/cloche/api/clochepb"
	"github.com/cloche-dev/cloche/internal/config"
	"github.com/cloche-dev/cloche/internal/domain"
	"github.com/cloche-dev/cloche/internal/logstream"
	"github.com/cloche-dev/cloche/internal/version"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	grpcStatus "google.golang.org/grpc/status"
)

func main() {
	if len(os.Args) < 2 {
		printTopLevelHelp()
		os.Exit(1)
	}

	// Pre-scan for --no-color before any other processing.
	for _, arg := range os.Args[1:] {
		if arg == "--no-color" {
			noColorFlag = true
			break
		}
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
	case "activity":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("activity")
			return
		}
		cmdActivity(os.Args[2:])
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
	case "doctor":
		if hasHelpFlag(os.Args[2:]) {
			printSubcommandHelp("doctor")
			return
		}
		cmdDoctor(os.Args[2:])
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
		"console": true,
	}
	if daemonCmds[os.Args[1]] && hasHelpFlag(os.Args[2:]) {
		printSubcommandHelp(os.Args[1])
		return
	}

	// Commands that need a daemon connection
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
	case "console":
		cmdConsole(client, os.Args[2:])
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

// parseResumeArg parses a resume argument which can be:
//   - a task ID ("cloche-k4gh") — no colons, resolved server-side
//   - a run ID ("pqpm-main") — no colons, resolved server-side
//   - a workflow ID ("a133:develop") — attempt:workflow
//   - a workflow ID ("TASK-123:a41k:develop") — task:attempt:workflow
//   - a step ID ("a133:develop:review") — attempt:workflow:step
//
// Returns taskOrRunID (for no-colon args) or compositeID (for colon args).
// The step name is embedded in the composite ID and extracted server-side.
func parseResumeArg(arg string) (taskOrRunID, compositeID string, err error) {
	if arg == "" {
		return "", "", fmt.Errorf("argument must not be empty")
	}
	if !strings.Contains(arg, ":") {
		// No colons — could be a task ID or a run ID. Let the server resolve it.
		return arg, "", nil
	}
	// Colon-separated composite ID: pass the full string for server-side resolution.
	// The server uses resolveRunIDFromID which handles:
	//   attempt:workflow, task:attempt, attempt:workflow:step, task:attempt:workflow,
	//   task:attempt:step (all formats).
	if strings.HasPrefix(arg, ":") {
		return "", "", fmt.Errorf("invalid argument %q: first component must not be empty", arg)
	}
	return "", arg, nil
}

func cmdResume(ctx context.Context, client pb.ClocheServiceClient, args []string) {
	if len(args) != 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche resume <task-id|run-id|workflow-id|step-id>\n")
		os.Exit(1)
	}

	taskOrRunID, compositeID, err := parseResumeArg(args[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Send resume via gRPC metadata on a RunWorkflow call.
	// Composite IDs (colon-separated) are resolved server-side via resolveRunIDFromID,
	// which handles attempt:workflow, task:attempt:workflow, attempt:workflow:step, etc.
	md := metadata.Pairs(
		"x-cloche-resume-run-id", compositeID,
		"x-cloche-resume-task-or-run", taskOrRunID,
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
		switch arg {
		case "--all":
			all = true
		case "--no-color":
			// handled globally in main()
		default:
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

	fmt.Printf("Task:    %s\n", colorID(resp.TaskId))
	if resp.Title != "" {
		fmt.Printf("Title:   %s\n", resp.Title)
	}
	fmt.Printf("Status:  %s\n", colorStatus(resp.Status))
	if resp.ProjectDir != "" {
		fmt.Printf("Project: %s\n", resp.ProjectDir)
	}

	if len(resp.Attempts) == 0 {
		fmt.Println("Attempt: none")
		return
	}

	latest := resp.Attempts[len(resp.Attempts)-1]
	fmt.Printf("Attempt: %s\n", colorID(latest.AttemptId))
	fmt.Printf("Result:  %s\n", colorStatus(latest.Result))
	if latest.EndedAt != "" && latest.EndedAt != "0001-01-01 00:00:00 +0000 UTC" {
		fmt.Printf("Ended:   %s\n", latest.EndedAt)
	}

	// If the task is waiting at a human step, surface the step name, elapsed
	// time since last poll, and poll count from the run's status.
	if resp.Status == "waiting" && latest.AttemptId != "" {
		if statusResp, err := client.GetStatus(ctx, &pb.GetStatusRequest{Id: latest.AttemptId}); err == nil && statusResp.WaitingStep != "" {
			elapsed := formatLastPollElapsed(statusResp.LastPollAt)
			if elapsed != "" {
				fmt.Printf("Waiting: %s — last polled %s ago (%d polls)\n", statusResp.WaitingStep, elapsed, statusResp.PollCount)
			} else {
				fmt.Printf("Waiting: %s (%d polls)\n", statusResp.WaitingStep, statusResp.PollCount)
			}
		}
	}

	// Show token usage across all attempts for this task.
	printTaskTokenUsage(ctx, client, taskID, resp.ProjectDir)
}

// printTaskTokenUsage fetches and displays total token consumption for all
// attempts of a task by querying the status of each attempt's run.
func printTaskTokenUsage(ctx context.Context, client pb.ClocheServiceClient, taskID, projectDir string) {
	usageResp, err := client.GetUsage(ctx, &pb.GetUsageRequest{
		ProjectDir: projectDir,
	})
	if err != nil || len(usageResp.Summaries) == 0 {
		return
	}

	var totalIn, totalOut int64
	agentTotals := map[string]int64{}
	for _, s := range usageResp.Summaries {
		totalIn += s.InputTokens
		totalOut += s.OutputTokens
		agentTotals[s.AgentName] = s.TotalTokens
	}
	total := totalIn + totalOut
	if total == 0 {
		return
	}

	// Format agent breakdown.
	var agents []string
	for agent, toks := range agentTotals {
		agents = append(agents, fmt.Sprintf("%s: %s", agent, formatTokenCount(toks)))
	}
	breakdown := strings.Join(agents, " / ")
	if breakdown != "" {
		fmt.Printf("Tokens:  %s (%s)\n", formatTokenCount(total), breakdown)
	} else {
		fmt.Printf("Tokens:  %s\n", formatTokenCount(total))
	}
}

// formatTokenCount formats a token count with comma separators for readability.
func formatTokenCount(n int64) string {
	if n == 0 {
		return "0"
	}
	s := fmt.Sprintf("%d", n)
	// Insert commas every 3 digits from the right.
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
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
		loopStatus = "running"
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

	// Active tasks with nested runs.
	printActiveTasks(ctx, client, w, projectDir)

	// Burn rate section: show per-agent token usage for the last hour.
	printBurnRate(ctx, client, w, projectDir)
}

func cmdStatusGlobal(ctx context.Context, client pb.ClocheServiceClient, w io.Writer) {
	// Server filters to past hour when All is not set.
	listResp, err := client.ListRuns(ctx, &pb.ListRunsRequest{})
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

	// Active tasks with nested runs.
	printActiveTasks(ctx, client, w, "")

	// Burn rate section: show per-agent token usage for the last hour.
	printBurnRate(ctx, client, w, "")
}

// printBurnRate fetches and displays per-agent token burn rates for the last hour.
// If no usage data exists, the section is omitted.
func printBurnRate(ctx context.Context, client pb.ClocheServiceClient, w io.Writer, projectDir string) {
	usageResp, err := client.GetUsage(ctx, &pb.GetUsageRequest{
		ProjectDir:    projectDir,
		WindowSeconds: 3600,
	})
	if err != nil || len(usageResp.Summaries) == 0 {
		return
	}

	// Only show section if there is actual data.
	var hasData bool
	for _, s := range usageResp.Summaries {
		if s.TotalTokens > 0 {
			hasData = true
			break
		}
	}
	if !hasData {
		return
	}

	fmt.Fprintf(w, "Token usage (last 1h):\n")
	for _, s := range usageResp.Summaries {
		if s.TotalTokens == 0 {
			continue
		}
		burnStr := fmt.Sprintf("~%.1fk/hr", s.BurnRate/1000)
		if s.BurnRate < 1000 {
			burnStr = fmt.Sprintf("~%.0f/hr", s.BurnRate)
		}
		fmt.Fprintf(w, "  %-10s %s in / %s out   %s total   %s\n",
			s.AgentName,
			formatTokenCount(s.InputTokens),
			formatTokenCount(s.OutputTokens),
			formatTokenCount(s.TotalTokens),
			burnStr,
		)
	}
}

// printActiveTasks displays active tasks with their in-progress runs nested under them.
func printActiveTasks(ctx context.Context, client pb.ClocheServiceClient, w io.Writer, projectDir string) {
	// Fetch all tasks (server returns all; we filter to running client-side).
	tasksResp, err := client.ListTasks(ctx, &pb.ListTasksRequest{
		ProjectDir: projectDir,
		All:        projectDir == "",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Fetch active runs (all time, to include long-running tasks) — both running and waiting.
	var allActiveRuns []*pb.RunSummary
	for _, state := range []string{"running", "waiting"} {
		runsResp, err := client.ListRuns(ctx, &pb.ListRunsRequest{
			State:      state,
			All:        true,
			ProjectDir: projectDir,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		allActiveRuns = append(allActiveRuns, runsResp.Runs...)
	}

	// Group runs by task ID.
	runsByTask := map[string][]*pb.RunSummary{}
	var noTaskRuns []*pb.RunSummary
	for _, run := range allActiveRuns {
		if run.TaskId != "" {
			runsByTask[run.TaskId] = append(runsByTask[run.TaskId], run)
		} else {
			noTaskRuns = append(noTaskRuns, run)
		}
	}

	// Filter to active tasks (running, waiting, or pending).
	var activeTasks []*pb.TaskSummary
	for _, task := range tasksResp.Tasks {
		if task.Status == "running" || task.Status == "waiting" || task.Status == "pending" {
			activeTasks = append(activeTasks, task)
		}
	}

	// Count total active: tasks with known task IDs plus orphan runs.
	knownTaskIDs := map[string]bool{}
	for _, task := range activeTasks {
		knownTaskIDs[task.TaskId] = true
	}
	orphanTaskCount := 0
	for taskID := range runsByTask {
		if !knownTaskIDs[taskID] {
			orphanTaskCount++
		}
	}
	totalActive := len(activeTasks) + orphanTaskCount + len(noTaskRuns)

	fmt.Fprintf(w, "Active tasks: %d\n", totalActive)
	for _, task := range activeTasks {
		title := task.Title
		if title == "" {
			title = task.TaskId
		}
		taskStatusStr := colorStatus(task.Status)
		fmt.Fprintf(w, "  %s [%s]: %s\n", colorID(task.TaskId), taskStatusStr, title)
		// Surface waiting step and time since last poll for waiting tasks.
		if task.WaitingStep != "" {
			elapsed := formatLastPollElapsed(task.LastPollAt)
			if elapsed != "" {
				fmt.Fprintf(w, "    Waiting: %s (last poll %s ago)\n", task.WaitingStep, elapsed)
			} else {
				fmt.Fprintf(w, "    Waiting: %s\n", task.WaitingStep)
			}
		}
		attemptID := task.LatestAttemptId
		if attemptID != "" {
			fmt.Fprintf(w, "    Attempt %d: %s\n", task.AttemptCount, colorID(attemptID))
			for _, run := range runsByTask[task.TaskId] {
				dur := formatDuration(run.StartedAt)
				runStatus := ""
				if run.State == "waiting" {
					runStatus = " [waiting]"
					if run.WaitingStep != "" {
						elapsed := formatLastPollElapsed(run.LastPollAt)
						if elapsed != "" {
							runStatus = fmt.Sprintf(" [waiting: %s, poll %s ago]", run.WaitingStep, elapsed)
						} else {
							runStatus = fmt.Sprintf(" [waiting: %s]", run.WaitingStep)
						}
					}
				}
				compositeID := fmt.Sprintf("%s:%s:%s", task.TaskId, attemptID, run.WorkflowName)
				if run.IsHost {
					fmt.Fprintf(w, "      %s : %s%s\n", colorID(compositeID), dur, runStatus)
				} else {
					fmt.Fprintf(w, "        - %s : %s%s\n", colorID(compositeID), dur, runStatus)
				}
			}
		} else {
			for _, run := range runsByTask[task.TaskId] {
				dur := formatDuration(run.StartedAt)
				fmt.Fprintf(w, "    %s: %s\n", run.WorkflowName, dur)
			}
		}
	}
	// Show runs whose task ID isn't in the active tasks list (orphans).
	for taskID, runs := range runsByTask {
		if !knownTaskIDs[taskID] {
			fmt.Fprintf(w, "  %s: (unknown)\n", colorID(taskID))
			for _, run := range runs {
				dur := formatDuration(run.StartedAt)
				fmt.Fprintf(w, "    %s: %s\n", run.WorkflowName, dur)
			}
		}
	}
	// Show runs with no task ID at all (legacy).
	for _, run := range noTaskRuns {
		dur := formatDuration(run.StartedAt)
		fmt.Fprintf(w, "  %s: %s\n", colorID(run.RunId), dur)
	}
}

// formatLastPollElapsed parses an RFC3339 timestamp and returns a human-readable
// duration since that time, or empty string if the timestamp is empty or invalid.
func formatLastPollElapsed(lastPollAt string) string {
	if lastPollAt == "" {
		return ""
	}
	parsed, err := time.Parse(time.RFC3339, lastPollAt)
	if err != nil {
		return ""
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
		status := task.Status
		if task.WaitingStep != "" {
			elapsed := formatLastPollElapsed(task.LastPollAt)
			if elapsed != "" {
				status = fmt.Sprintf("%s [%s, poll %s ago]", task.Status, task.WaitingStep, elapsed)
			} else {
				status = fmt.Sprintf("%s [%s]", task.Status, task.WaitingStep)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			task.TaskId, status, task.AttemptCount, latestAttempt, title)
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
		case "log_chunk":
			// Continuation chunk from a chunked log response (large files).
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
		fmt.Fprintf(os.Stderr, "usage: cloche stop <task-id>\n")
		os.Exit(1)
	}

	_, err := client.StopRun(ctx, &pb.StopRunRequest{TaskId: args[0]})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Stopped task: %s\n", args[0])
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

	// Check for "once" subcommand
	if len(args) > 0 && args[0] == "once" {
		cmdLoopOnce(ctx, client, cwd)
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

// cmdLoopOnce enables the orchestration loop with max_concurrent=1, waits for
// exactly one run to reach a terminal state, then disables the loop.
func cmdLoopOnce(ctx context.Context, client pb.ClocheServiceClient, projectDir string) {
	// Snapshot current run IDs so we can detect a new one.
	existing := make(map[string]bool)
	if resp, err := client.ListRuns(ctx, &pb.ListRunsRequest{ProjectDir: projectDir}); err == nil {
		for _, r := range resp.Runs {
			existing[r.RunId] = true
		}
	}

	// Enable loop with max_concurrent=1.
	_, err := client.EnableLoop(ctx, &pb.EnableLoopRequest{
		ProjectDir:    projectDir,
		MaxConcurrent: 1,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Orchestration loop started (once mode).")

	// Run the polling loop; stopLoop ensures the loop is always disabled.
	exitCode := loopOnceWait(ctx, client, projectDir, existing)

	_, _ = client.DisableLoop(ctx, &pb.DisableLoopRequest{ProjectDir: projectDir})
	fmt.Println("Orchestration loop stopped.")
	os.Exit(exitCode)
}

// loopOnceWait polls until a new run reaches a terminal state. Returns 0 on
// success, 1 on failure/cancellation/error.
func loopOnceWait(ctx context.Context, client pb.ClocheServiceClient, projectDir string, existing map[string]bool) int {
	pollInterval := 3 * time.Second
	for {
		time.Sleep(pollInterval)

		resp, err := client.ListRuns(ctx, &pb.ListRunsRequest{ProjectDir: projectDir})
		if err != nil {
			fmt.Fprintf(os.Stderr, "error polling runs: %v\n", err)
			return 1
		}

		for _, r := range resp.Runs {
			if existing[r.RunId] {
				continue
			}
			// Found a new run — check its state.
			switch domain.RunState(r.State) {
			case domain.RunStateSucceeded:
				fmt.Printf("Run %s succeeded.\n", r.RunId)
				return 0
			case domain.RunStateFailed:
				fmt.Printf("Run %s failed.\n", r.RunId)
				return 1
			case domain.RunStateCancelled:
				fmt.Printf("Run %s cancelled.\n", r.RunId)
				return 1
			}
			// Still pending/running — keep polling.
		}
	}
}

func cmdTasks(args []string) {
	httpAddr := resolveHTTPAddr()
	if httpAddr == "" {
		httpAddr = "localhost:8080"
	}

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
	var force, restart bool
	for _, a := range args {
		switch a {
		case "-f", "--force":
			force = true
		case "-r", "--restart":
			restart = true
		}
	}

	daemonWasRunning := true
	_, err := client.Shutdown(ctx, &pb.ShutdownRequest{Force: force})
	if err != nil {
		if restart && grpcStatus.Code(err) == codes.Unavailable {
			// Daemon isn't running; skip straight to launch.
			daemonWasRunning = false
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
	} else {
		fmt.Println("Daemon shutting down.")
	}

	if restart {
		if daemonWasRunning {
			// Give the daemon a moment to release the port.
			time.Sleep(500 * time.Millisecond)
		}
		daemonPath, err := findDaemonBinary()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if err := launchDaemon(daemonPath); err != nil {
			fmt.Fprintf(os.Stderr, "error launching daemon: %v\n", err)
			os.Exit(1)
		}
		fmt.Println("Daemon launched.")
	}
}

// findDaemonBinary returns the path to cloched, expected to live next to the
// current executable.
func findDaemonBinary() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("cannot locate current executable: %w", err)
	}
	return filepath.Join(filepath.Dir(exe), "cloched"), nil
}

// launchDaemon starts cloched at daemonPath as a detached process (new
// session) so the daemon outlives the CLI process.
func launchDaemon(daemonPath string) error {
	cmd := exec.Command(daemonPath)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Start()
}

// resolveRunContext returns the task ID, attempt ID, and run ID for KV context commands.
// All come from environment variables set by the daemon when launching host steps.
func resolveRunContext() (taskID, attemptID, runID string, err error) {
	taskID = os.Getenv("CLOCHE_TASK_ID")
	if taskID == "" {
		return "", "", "", fmt.Errorf("CLOCHE_TASK_ID environment variable is not set")
	}
	attemptID = os.Getenv("CLOCHE_ATTEMPT_ID")
	runID = os.Getenv("CLOCHE_RUN_ID")
	return taskID, attemptID, runID, nil
}

// dialDaemon creates a gRPC connection to the daemon using CLOCHE_ADDR or the
// default socket address.
func dialDaemon() (*grpc.ClientConn, error) {
	addr := os.Getenv("CLOCHE_ADDR")
	if addr == "" {
		addr = config.DefaultAddr()
	}
	return grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func cmdGet(args []string) {
	if len(args) < 1 {
		fmt.Fprintf(os.Stderr, "usage: cloche get <key>\n")
		os.Exit(1)
	}

	taskID, attemptID, runID, err := resolveRunContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	conn, err := dialDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error connecting to daemon: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.GetContextKey(ctx, &pb.GetContextKeyRequest{
		TaskId:    taskID,
		AttemptId: attemptID,
		RunId:     runID,
		Key:       args[0],
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if !resp.Found {
		fmt.Fprintf(os.Stderr, "key not found: %s\n", args[0])
		os.Exit(1)
	}
	fmt.Println(resp.Value)
}

func cmdSet(args []string) {
	if len(args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: cloche set <key> <value>\n")
		fmt.Fprintf(os.Stderr, "       cloche set <key> -     (read value from stdin)\n")
		fmt.Fprintf(os.Stderr, "       cloche set <key> -f <file>\n")
		os.Exit(1)
	}

	taskID, attemptID, runID, err := resolveRunContext()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	key := args[0]
	var value string
	switch {
	case args[1] == "-f" && len(args) >= 3:
		data, err := os.ReadFile(args[2])
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading file: %v\n", err)
			os.Exit(1)
		}
		value = string(data)
	case args[1] == "-":
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading stdin: %v\n", err)
			os.Exit(1)
		}
		value = strings.TrimRight(string(data), "\n")
	default:
		value = args[1]
	}

	conn, err := dialDaemon()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error connecting to daemon: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	client := pb.NewClocheServiceClient(conn)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = client.SetContextKey(ctx, &pb.SetContextKeyRequest{
		TaskId:    taskID,
		AttemptId: attemptID,
		RunId:     runID,
		Key:       key,
		Value:     value,
	})
	if err != nil {
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
		addr = config.DefaultAddr()
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
