package main

import (
	"fmt"
	"os"
)

// subcommandHelp maps each subcommand to its detailed help text.
// Help text is formatted for LLM consumption: structured, explicit, with examples.
var subcommandHelp = map[string]string{
	"init": `cloche init — Initialize or re-register a Cloche project

The bare command is safe to run on any existing project: it ensures
the .cloche/ directory exists, creates or updates .cloche/config.toml
(setting active = true), adds .gitignore entries for runtime state, and
writes the global daemon config if absent.

Use --new for first-time setup to generate workflow files, Dockerfile,
and prompt/script templates.  Use --install-shell-helpers once per
machine to install shell tab-completion.

Usage:
  cloche init [-n | --new] [--install-shell-helpers]
              [--workflow <name>] [--base-image <image>]
              [--agent-command <cmd>] [--no-llm]

Flags:
  -n, --new                 Generate workflow files, Dockerfile, prompts, and
                            scripts (first-time project setup). Existing files
                            are skipped.
  --install-shell-helpers   Install bash/zsh completion scripts and add a
                            source line to the shell rc file. One-time per
                            machine, not per project.
  --workflow <name>         Workflow file name for --new (default: "develop")
  --base-image <image>      Base Docker image for --new Dockerfile
                            (default: "cloche-agent:latest")
  --agent-command <cmd>     LLM command for the --new analysis phase
                            (overrides config and env)
  --no-llm                  Skip the LLM-assisted placeholder filling phase

Core behavior (always, no flags needed):
  .cloche/                   Directory created if missing
  .cloche/config.toml        Created with active = true, or updated in-place
  .gitignore                 Runtime-state entries added if missing
  ~/.config/cloche/config    Global daemon config created if absent

Additional files created by --new (skipped if already exist):
  .cloche/<name>.cloche              Workflow definition
  .cloche/host.cloche                Host orchestration workflow
  .cloche/Dockerfile                 Container image definition
  .cloche/prompts/implement.md       Prompt for the implement step
  .cloche/prompts/fix-tests.md       Prompt for the fix-tests step
  .cloche/prompts/fix-merge.md       Prompt for resolving merge conflicts
  .cloche/scripts/get-tasks.py       Read next open task from task tracker
  .cloche/scripts/claim-task.py      Mark task as in-progress
  .cloche/scripts/prepare-merge.py   Create worktree and rebase agent branch
  .cloche/scripts/merge.py           Fast-forward base branch to agent branch
  .cloche/scripts/release-task.py    Mark completed task as done
  .cloche/scripts/cleanup.py         Clean up worktree and branch
  .cloche/scripts/unclaim.py         Reset task to open and stop loop
  .cloche/version                    Schema version marker

LLM command resolution order (--new only):
  1. --agent-command flag
  2. CLOCHE_AGENT_COMMAND environment variable
  3. Global config [daemon] llm_command
  4. claude if available on PATH
  Falls back gracefully with a warning if no LLM is available.

Examples:
  cloche init                                      # register existing project
  cloche init --new                                # first-time project setup
  cloche init --new --workflow build --base-image python:3.12
  cloche init --install-shell-helpers              # one-time shell completion setup
`,

	"doctor": `cloche doctor — Diagnose Cloche infrastructure

Checks every layer of the setup stack in order and prints a status line for
each check. Exits with code 1 if any check fails.

Checks performed:
  1. Docker daemon reachable
  2. Base image exists (cloche-base:latest or cloche-agent:latest)
  3. Daemon reachable via gRPC (GetVersion)
  4. Agent authentication credentials present (soft check — warning only)

Usage:
  cloche doctor [--verbose]

Flags:
  --verbose    Print details for all checks, including timing and config values

Examples:
  cloche doctor
  cloche doctor --verbose
`,

	"health": `cloche health — Show project health summary

Queries the daemon's HTTP API for a summary of all registered projects
and their pass/fail test counts.

Usage:
  cloche health

Environment:
  CLOCHE_HTTP    Daemon HTTP address (e.g. "localhost:8080"). Required.

Examples:
  export CLOCHE_HTTP=localhost:8080
  cloche health
`,

	"run": `cloche run — Launch a workflow run

Starts a new workflow run. Without --issue, a User-Initiated task is created
automatically and a task ID is printed alongside the run ID.

Usage:
  cloche run <workflow>[:<step>] [--prompt "..."] [--title "..."] [--issue ID] [--keep-container]

Arguments:
  <workflow>           Name of the workflow to run. Must match a
                       .cloche/<workflow>.cloche file in the project.
  <workflow>:<step>    Run starting at a specific step within the workflow.
                       Execution begins at <step> instead of the entry step.

Flags:
  --prompt "..."       Prompt text passed to agent steps. Also available as
  -p "..."             the short form.
  --title "..."        Human-readable title for the run (shown in status/list).
  --issue ID, -i ID    Associate an existing task/issue ID with the run.
                       Without this flag, a User-Initiated task is created.
  --keep-container     Do not remove the container after the run completes.
                       Useful for debugging.

The command prints the workflow ID, task ID, and attempt ID on success.
Use the task ID with "cloche status", "cloche logs", and "cloche list".

Examples:
  cloche run develop --prompt "Add a /health endpoint"
  cloche run develop -p "Fix the broken CSV parser" --title "CSV fix"
  cloche run develop:review -p "Check the implementation"
  cloche run build --keep-container
  cloche run develop -p "Fix auth bug" -i TASK-123
`,

	"resume": `cloche resume — Resume a failed workflow run

Re-attempts a failed workflow run from a specific step. The container
must still be available for container workflows (failed runs keep their
containers by default).

Usage:
  cloche resume <task-id>
  cloche resume <workflow-id>
  cloche resume <step-id>

Arguments:
  <task-id>      Task identifier (e.g. TASK-123 or cloche-k4gh).
                 Finds and resumes the latest failed run for that task.
  <workflow-id>  Colon-separated workflow identifier. Accepted formats:
                   attempt:workflow         (e.g. a133:develop)
                   task:attempt:workflow    (e.g. TASK-123:a41k:develop)
                 Resumes from the first failed step.
  <step-id>      Colon-separated step identifier. Accepted formats:
                   attempt:workflow:step    (e.g. a133:develop:review)
                 Resumes from that specific step.

Step-specific resume behavior:
  script step    Reruns the script fresh. Updated scripts are picked up.
  prompt step    Resumes the conversation (Claude: -c flag with "retry"
                 prompt) instead of starting a new one.
  workflow step  Same as script — starts the step again, passing values
                 from previous steps' output.

Prerequisites:
  - The workflow run must be in a failed state.
  - For container workflows, the container must still exist.

Examples:
  cloche resume TASK-123
  cloche resume a133:develop
  cloche resume TASK-123:a41k:develop
  cloche resume a133:develop:implement
`,

	"status": `cloche status — Check task or daemon status

Without an ID, shows a daemon status overview for the current project.
With a task ID, shows the latest attempt status for that task.

Usage:
  cloche status [<task-id>] [--all]

Arguments:
  <task-id>   A task ID. When omitted, shows a daemon status overview.

Flags:
  --all        Show global stats instead of project-specific stats (overview mode).
  --no-color   Disable ANSI color output (also respects NO_COLOR env var).

Output (task ID):
  Task        Task identifier
  Title       Human-readable title (if set)
  Status      Current task status
  Project     Project directory
  Attempt     Latest attempt ID
  Result      running, succeeded, failed, or cancelled
  Ended       End timestamp (if complete)

Output (no ID — daemon overview):
  Daemon version
  Project name and concurrency (if in project directory)
  Orchestration loop status
  Successful / total runs in the past hour
  Active run count with per-run duration

Examples:
  cloche status TASK-123
  cloche status
  cloche status --all
`,

	"logs": `cloche logs — Show logs for a task, attempt, workflow run, or step

Streams or displays log output. The first argument accepts any level of
the ID hierarchy.

Usage:
  cloche logs <id> [--type <full|script|llm>] [-f] [-l <n>]

Arguments:
  <id>    Any of the following:
            Task ID        (shandalar-1234)        — logs for the latest attempt
            Attempt ID     (a3f7)                  — logs for that attempt
            Workflow ID    (a3f7:develop)           — logs for that workflow run
            Step ID        (a3f7:develop:implement) — logs for that step
          Legacy composite task:attempt[:step] is also accepted.

Flags:
  --type <full|script|llm>       Filter by log type:
                                   full    — complete unfiltered output
                                   script  — script/command output only
                                   llm     — LLM interaction logs only
  --follow, -f                   Stream logs in real time (blocks until the
                                 run completes or is stopped).
  --limit, -l <n>                Display only the last n lines of output.

Flags are combinable: cloche logs a3f7:develop:implement -l 20 -f

Examples:
  cloche logs TASK-123
  cloche logs a3f7
  cloche logs a3f7:develop
  cloche logs a3f7:develop:implement
  cloche logs TASK-123:a3f7
  cloche logs TASK-123:a3f7:implement
  cloche logs a3f7 --type script
  cloche logs a3f7:develop -f -l 50
`,

	"poll": `cloche poll — Wait for runs or steps to finish

Polls the daemon every 2 seconds until all targets reach a terminal state
(succeeded, failed, or cancelled).

Accepts any level of the ID hierarchy:
  task ID        shandalar-1234          — most recent run for the task
  attempt ID     a133                    — run for that attempt
  workflow ID    a133:develop            — specific workflow run
  step ID        a133:develop:review     — specific step within a run

Polling a step ID waits until that step completes, then exits 0 — useful
for waiting on a long-running step without waiting for the whole run.

With a single ID, prints step-level progress.
With multiple IDs, displays a compact status summary.

Usage:
  cloche poll <id> [id...] [--no-color]

Arguments:
  <id>    One or more IDs at any level of the hierarchy.

Flags:
  --no-color   Disable ANSI color output (also respects NO_COLOR env var).

Exit codes:
  0    All runs (or steps) succeeded.
  1    Any run failed, was cancelled, or the container died.

Examples:
  cloche poll a133
  cloche poll a133:develop
  cloche poll a133:develop:review
  cloche poll shandalar-1234
  cloche poll abc123 def456 ghi789
`,

	"list": `cloche list — List tasks

Shows all tasks for the current project, grouped by status with attempt
count and latest attempt ID. Use --all to show tasks from all projects,
or --runs to show a flat run listing instead.

Usage:
  cloche list [flags]

Flags:
  --all              Show tasks from all projects (default: current project only).
  --project, -p DIR  Filter by project directory.
  --state, -s STATE  Filter by task status (pending, running, succeeded, failed, cancelled).
  --limit, -n NUM    Limit the number of results returned.
  --runs             Show flat run listing instead of task-oriented view.

Output columns (default): task ID, status, attempt count, latest attempt ID, title.
Output columns (--runs):   workflow ID, workflow, state, type, task ID, title, error.

Examples:
  cloche list
  cloche list --all
  cloche list --state running
  cloche list --limit 10
  cloche list --all --state failed --limit 5
  cloche list -p /home/user/project -s succeeded -n 20
  cloche list --runs
`,

	"stop": `cloche stop — Stop all active runs for a task

Sends a stop signal to all active runs belonging to the given task.
Each container is terminated and its run state transitions to "cancelled".

Usage:
  cloche stop <task-id>

Arguments:
  <task-id>    The task identifier.

Examples:
  cloche stop TASK-42
`,

	"delete": `cloche delete — Delete a retained container

Removes a container that was kept after a run (via --keep-container).
Accepts either a container ID or a run ID.

Usage:
  cloche delete <container-or-run-id>

Arguments:
  <container-or-run-id>    Container ID or run ID to delete.

Examples:
  cloche delete abc123
`,

	"tasks": `cloche tasks — Show task pipeline and assignment state

Queries the daemon's HTTP API for the current task list, showing which
tasks are open, assigned, or completed.

Usage:
  cloche tasks [--project <dir>]

Flags:
  --project <dir>    Project directory name to query (default: current
                     directory basename).

Environment:
  CLOCHE_HTTP    Daemon HTTP address (default: "localhost:8080").

Output columns: ID, STATUS, ASSIGNED, RUN, TITLE

Examples:
  cloche tasks
  cloche tasks --project my-app
`,

	"loop": `cloche loop — Manage the orchestration loop

Starts or stops the daemon's orchestration loop, which automatically
picks up and runs tasks from the task pipeline.

Usage:
  cloche loop [--max <n>]     Start the orchestration loop
  cloche loop once            Run one task then stop the loop
  cloche loop stop            Stop the orchestration loop

Flags:
  --max <n>    Maximum number of concurrent runs (default: value from
               .cloche/config.toml).

The "once" subcommand starts the loop, waits for a single task to be
picked up and completed, then automatically stops the loop. Exits 0
on success, 1 on failure or cancellation.

When stop_on_error or max_consecutive_failures is configured in
.cloche/config.toml, an unrecovered error will stop the loop. Run
"cloche loop" again to restart it.

Examples:
  cloche loop
  cloche loop --max 3
  cloche loop once
  cloche loop stop
`,

	"get": `cloche get — Get a value from the run context store

Reads a key from the task's context.json file. Intended for use inside
workflow scripts and steps.

Usage:
  cloche get <key>

Arguments:
  <key>    The context key to read.

Environment:
  CLOCHE_TASK_ID       Task identifier (required).
  CLOCHE_PROJECT_DIR   Project directory (default: current directory).

Exit codes:
  0    Key exists; value printed to stdout.
  1    Key not found or error.

Examples:
  cloche get branch
  cloche get child_run_id
`,

	"set": `cloche set — Set a value in the run context store

Writes a key-value pair to the task's context.json file. Use "-" as the
value to read from stdin (useful for multi-line content).

Usage:
  cloche set <key> <value>
  cloche set <key> -          (read value from stdin)

Arguments:
  <key>      The context key to write.
  <value>    The value to store, or "-" to read from stdin.

Environment:
  CLOCHE_TASK_ID       Task identifier (required).
  CLOCHE_PROJECT_DIR   Project directory (default: current directory).

Examples:
  cloche set branch feature-auth
  echo "multi-line content" | cloche set notes -
`,

	"workflow": `cloche workflow — View workflow definitions

Lists all workflows in the project or renders a specific workflow as an
ASCII-art graph showing steps, wiring, and result paths.

Usage:
  cloche workflow [--project <dir>]          List all workflows
  cloche workflow <name> [--project <dir>]   Show workflow graph

Arguments:
  <name>    Name of the workflow to render as a graph.

Flags:
  --project <dir>, -p <dir>    Project directory (default: current directory).

When listing, workflows are grouped by type (container or host). When
showing a specific workflow, the output is a graph with step boxes and
colored wires: green for success, red for fail/failed, and
blue/yellow/orange/magenta for other result paths. Wires to the same
destination are merged for readability.

Examples:
  cloche workflow
  cloche workflow develop
  cloche workflow main -p /path/to/project
  cloche workflow --project ../other-project
`,

	"project": `cloche project — Show project info and config

Displays project-level information including config settings, orchestrator
loop state, concurrency, active runs, and known workflows.

By default, looks up the project by the current working directory. Use
--name to look up a project by its label instead.

Usage:
  cloche project [--name <label>]

Flags:
  --name <label>    Look up project by label (e.g. "cloche") instead of
                    the current directory.

Output includes:
  Config            active, concurrency, stagger, dedup, stop_on_error,
                    evolution settings
  Loop              Orchestration loop state (running, stopped, or halted)
  Active runs       Currently pending or running workflow runs
  Workflows         Known container and host workflow names

Environment:
  CLOCHE_ADDR    Daemon gRPC address (default: 127.0.0.1:50051)

Examples:
  cloche project
  cloche project --name cloche
  cloche project --name my-app
`,

	"validate": `cloche validate — Validate project configuration and workflows

Parses and validates all config and workflow files in the project's .cloche/
directory. Checks syntax, result wiring, terminal coverage, orphan steps,
file references, and cross-file consistency.

Usage:
  cloche validate [--project <path>] [--workflow <name>]

Flags:
  --project <path>    Project directory to validate (default: current directory).
  --workflow <name>   Validate only the named workflow instead of all workflows.

Checks performed:
  config.toml         Parses correctly, fields are valid.
  Workflow files      Syntax, result wiring completeness, terminal coverage
                      (all paths reach done/abort), no orphan steps, and
                      config key validation.
  File references     prompt file() paths resolve to .cloche/prompts/,
                      script run paths resolve to .cloche/scripts/.
  Cross-file          workflow_name references resolve to defined workflows.

Exit codes:
  0    All configuration valid.
  1    One or more errors found.

Examples:
  cloche validate
  cloche validate --project /path/to/project
  cloche validate --workflow develop
`,

	"console": `cloche console — Start an interactive agent session in a container

Launches a fresh container from the project's Docker image (same setup as a
workflow run: project files copied in, auth credentials injected, overrides
applied), starts the agent command with a TTY, and connects your terminal
bidirectionally. The container is always kept after the session ends.

Usage:
  cloche console [--agent <command>]

Flags:
  --agent <command>    Override the agent command to run inside the container.
                       Defaults to the agent resolution chain:
                         1. workflow-level container.agent_command
                         2. CLOCHE_AGENT_COMMAND environment variable
                         3. Default: "claude"

Must be run from inside a git repository with a .cloche/ directory.

On exit, the container ID is printed so you can inspect or clean up:
  docker exec -it <id> bash
  docker cp <id>:/workspace/file.txt .
  cloche delete <id>

Exit codes:
  Reflects the agent's exit code from inside the container.

Examples:
  cloche console
  cloche console --agent bash
  cloche console --agent "claude --model claude-opus-4-6"
`,

	"activity": `cloche activity — Show project activity log

Reads the project's .cloche/activity.log file and displays attempt and step
lifecycle events: when tasks were attempted, which steps fired, their
timestamps, and the outcome of each step.

Usage:
  cloche activity [--project <dir>] [--since <duration|time>] [--until <time>] [--json]

Flags:
  --project <dir>, -p <dir>    Project directory (default: current directory).
  --since <value>              Show only entries on or after this time.
                               Accepts a Go duration (e.g. "24h", "7d", "30m")
                               or an RFC3339 timestamp.
  --until <time>               Show only entries on or before this RFC3339 time.
  --json                       Output raw JSONL instead of the table view.

Output columns:
  TIME        Local timestamp of the event.
  KIND        attempt_started, attempt_ended, step_started, or step_completed.
  TASK        Task ID (if known).
  ATTEMPT     Attempt ID.
  WORKFLOW    Workflow name (for step events).
  STEP        Step name (for step events).
  OUTCOME     Result (for step_completed) or state (for attempt_ended).

The activity log is written to .cloche/activity.log. It is created automatically
when the orchestration loop or a host workflow run starts and is intended to
help diagnose which paths through the workflow graph were taken.

Examples:
  cloche activity
  cloche activity --since 24h
  cloche activity --since 7d
  cloche activity --since 2026-03-01T00:00:00Z
  cloche activity --project /path/to/project
  cloche activity --json
`,

	"shutdown": `cloche shutdown — Shut down the daemon

Sends a shutdown signal to the Cloche daemon. Refuses to shut down if
there are active runs unless --force is specified.

Usage:
  cloche shutdown [--force|-f] [--restart|-r]

Flags:
  -f, --force     Shut down even if runs are still active.
  -r, --restart   Relaunch the daemon after stopping it (or start it if
                  it is not already running). The new daemon process is
                  detached so the CLI can exit immediately.

Examples:
  cloche shutdown
  cloche shutdown --force
  cloche shutdown --restart
  cloche shutdown --force --restart
`,
}

// printHelp prints the top-level help or subcommand-specific help.
// Returns true if help was printed (caller should exit).
func printHelp(args []string) bool {
	if len(args) == 0 {
		printTopLevelHelp()
		return true
	}

	// "cloche help <subcommand>"
	cmd := args[0]
	if text, ok := subcommandHelp[cmd]; ok {
		fmt.Fprint(os.Stderr, text)
		return true
	}

	fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", cmd)
	printTopLevelHelp()
	return true
}

// hasHelpFlag returns true if the args contain --help or -h.
func hasHelpFlag(args []string) bool {
	for _, arg := range args {
		if arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

// printSubcommandHelp prints help for a subcommand and exits.
func printSubcommandHelp(cmd string) {
	if text, ok := subcommandHelp[cmd]; ok {
		fmt.Fprint(os.Stderr, text)
	} else {
		fmt.Fprintf(os.Stderr, "no help available for %q\n", cmd)
	}
}

func printTopLevelHelp() {
	fmt.Fprint(os.Stderr, `cloche — Grow-code high quality applications

Cloche provides containerized environments for coding agents, a workflow DSL
for linking agentic and script-driven tasks, and validated code pipelines.

Usage:
  cloche <command> [args]
  cloche help <command>       Show detailed help for a command
  cloche <command> --help     Same as above

Project Setup:
  init       Initialize a Cloche project (.cloche/ directory and templates)
  doctor     Diagnose infrastructure (Docker, base image, daemon, agent auth)
  health     Show project health summary (pass/fail counts)
  project    Show project info, config, loop state, and workflows

Workflow Info:
  workflow   List workflows or show a workflow as an ASCII-art graph
  validate   Validate project configuration and workflow definitions

Workflow Runs:
  run        Launch a workflow run in a container
  resume     Resume a failed workflow run from a specific step
  status     Show daemon overview or check a specific run's status
  logs       Show or stream logs for a run
  poll       Wait for one or more runs to finish (blocks until terminal)
  list       List runs for current project (or all projects)
  stop       Stop a running workflow
  delete     Delete a retained container
  console    Start an interactive agent session in a container

Orchestration:
  tasks      Show task pipeline and assignment state
  loop       Start or stop the orchestration loop
  activity   Show project activity log (attempt/step timestamps and outcomes)

Context Store (for use inside workflow steps):
  get        Get a value from the run context store
  set        Set a value in the run context store

Daemon:
  shutdown   Shut down the Cloche daemon

Environment Variables:
  CLOCHE_ADDR          Daemon gRPC address (default: 127.0.0.1:50051)
  CLOCHE_HTTP          Daemon HTTP address (for health/tasks commands)
  CLOCHE_RUN_ID        Workflow ID for the current run (set automatically in steps)
  CLOCHE_PROJECT_DIR   Project directory override for get/set commands

Examples:
  cloche init
  cloche run develop --prompt "Add user authentication"
  cloche status abc123
  cloche logs abc123 --follow
  cloche list
  cloche loop --max 2
`)
}
