package main

import (
	"fmt"
	"os"
)

// subcommandHelp maps each subcommand to its detailed help text.
// Help text is formatted for LLM consumption: structured, explicit, with examples.
var subcommandHelp = map[string]string{
	"init": `cloche init — Initialize a Cloche project

Creates the .cloche/ directory with a workflow definition, Dockerfile,
prompt templates, and configuration files.

Usage:
  cloche init [--workflow <name>] [--base-image <image>]

Flags:
  --workflow <name>       Name for the workflow file (default: "develop")
  --base-image <image>    Base Docker image for the Dockerfile (default: "cloche-base:latest")

What it creates:
  .cloche/<name>.cloche           Workflow definition
  .cloche/host.cloche             Host orchestration workflow
  .cloche/Dockerfile              Container image definition
  .cloche/config.toml             Project configuration
  .cloche/prompts/implement.md    Prompt for the implement step
  .cloche/prompts/fix.md          Prompt for the fix step
  .cloche/prompts/update-docs.md  Prompt for the update-docs step
  .cloche/scripts/prepare-prompt.sh  Prompt generation script
  .cloche/version                 Schema version marker

Existing files are never overwritten. Run this command again safely to add
missing files without losing customizations.

Examples:
  cloche init
  cloche init --workflow build --base-image python:3.12
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

Starts a new container workflow run. The daemon builds/pulls the container
image, copies the project into it, and executes the workflow steps.

Usage:
  cloche run --workflow <name> [--prompt "..."] [--title "..."] [--issue ID] [--keep-container]
  cloche run <name>              (shorthand: bare positional workflow name)

Flags:
  --workflow <name>    Name of the workflow to run (required). Must match a
                       .cloche/<name>.cloche file in the project.
  --prompt "..."       Prompt text passed to agent steps. Also available as
  -p "..."             the short form.
  --title "..."        Human-readable title for the run (shown in status/list).
  --issue ID, -i ID    Associate a task/issue ID with the run (shown in list).
  --keep-container     Do not remove the container after the run completes.
                       Useful for debugging.

The command prints the run ID on success. Use that ID with status, logs,
poll, and stop.

Examples:
  cloche run --workflow develop --prompt "Add a /health endpoint"
  cloche run develop -p "Fix the broken CSV parser" --title "CSV fix"
  cloche run --workflow build --keep-container
  cloche run develop -p "Fix auth bug" -i TASK-123
`,

	"status": `cloche status — Check run status

Shows the current state of a workflow run including its type, active step,
and the result of each completed step.

Usage:
  cloche status <run-id>

Arguments:
  <run-id>    The run identifier returned by "cloche run".

Output fields:
  Run         Run identifier
  Title       Human-readable title (if set)
  Workflow    Workflow name
  Type        "host" or "container"
  State       Current state (e.g. running, succeeded, failed, cancelled)
  Container   Truncated container ID (container runs only)
  Error       Error message (if failed)
  Active      Name of the currently executing step
  Steps       List of completed steps with results and timestamps

Examples:
  cloche status abc123
`,

	"logs": `cloche logs — Show logs for a run

Streams or displays log output for a workflow run. Supports filtering by
step name and log type, limiting output, and following live output.

Usage:
  cloche logs <run-id> [-s <name>] [--type <full|script|llm>] [-f] [-l <n>]

Arguments:
  <run-id>    The run identifier.

Flags:
  --step, -s <name>              Show logs only for the named step.
  --type <full|script|llm>       Filter by log type:
                                   full    — complete unfiltered output
                                   script  — script/command output only
                                   llm     — LLM interaction logs only
  --follow, -f                   Stream logs in real time (blocks until the
                                 run completes or is stopped).
  --limit, -l <n>                Display only the last n lines of output.

Flags are combinable: cloche logs run-id -s implement -l 20 -f

Examples:
  cloche logs abc123
  cloche logs abc123 -s implement
  cloche logs abc123 -s implement -l 20
  cloche logs abc123 -f
  cloche logs abc123 -s test --type script
  cloche logs abc123 -s implement -l 20 -f
`,

	"poll": `cloche poll — Wait for runs to finish

Polls the daemon every 2 seconds until all runs reach a terminal state
(succeeded, failed, or cancelled).

With a single run ID, prints step-level progress (same as before).
With multiple run IDs, displays a compact status summary.

Usage:
  cloche poll <run-id> [run-id...]

Arguments:
  <run-id>    One or more run identifiers.

Exit codes:
  0    All runs succeeded.
  1    Any run failed, was cancelled, or the container died.

Examples:
  cloche poll abc123
  cloche poll abc123 def456 ghi789
  cloche run develop -p "Fix bug" && cloche poll "$(cloche run develop -p 'Fix bug')"
`,

	"list": `cloche list — List workflow runs

Shows all runs for the current project, or all runs across all projects
with --all. Results can be filtered by state, project, issue, or limited
to a fixed number.

Usage:
  cloche list [flags]

Flags:
  --all              Show runs from all projects (default: current project only).
  --project, -p DIR  Filter by project directory.
  --state, -s STATE  Filter by run state (pending, running, succeeded, failed, cancelled).
  --limit, -n NUM    Limit the number of results returned.
  --issue, -i ID     Filter by issue/task ID.

Output columns: run ID, workflow name, state, type (host/container),
task ID, title, container ID, and error message (if any).

Examples:
  cloche list
  cloche list --all
  cloche list --state running
  cloche list --limit 10
  cloche list --all --state failed --limit 5
  cloche list --issue TASK-123
  cloche list -p /home/user/project -s succeeded -n 20
`,

	"stop": `cloche stop — Stop a running workflow

Sends a stop signal to a running workflow. The container is terminated
and the run state transitions to "cancelled".

Usage:
  cloche stop <run-id>

Arguments:
  <run-id>    The run identifier.

Examples:
  cloche stop abc123
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

Starts, stops, or resumes the daemon's orchestration loop, which
automatically picks up and runs tasks from the task pipeline.

Usage:
  cloche loop [--max <n>]     Start the orchestration loop
  cloche loop stop            Stop the orchestration loop
  cloche loop resume          Resume a halted loop (clear error state)

Flags:
  --max <n>    Maximum number of concurrent runs (default: value from
               .cloche/config.toml).

When stop_on_error is enabled in .cloche/config.toml, an unrecovered
error will halt the loop. Use "cloche loop resume" to clear the error
and resume picking up new work.

Examples:
  cloche loop
  cloche loop --max 3
  cloche loop stop
  cloche loop resume
`,

	"get": `cloche get — Get a value from the run context store

Reads a key from the run's context.json file. Intended for use inside
workflow scripts and steps.

Usage:
  cloche get <key>

Arguments:
  <key>    The context key to read.

Environment:
  CLOCHE_RUN_ID        Run identifier (required).
  CLOCHE_PROJECT_DIR   Project directory (default: current directory).

Exit codes:
  0    Key exists; value printed to stdout.
  1    Key not found or error.

Examples:
  cloche get branch
  cloche get task_id
`,

	"set": `cloche set — Set a value in the run context store

Writes a key-value pair to the run's context.json file. Use "-" as the
value to read from stdin (useful for multi-line content).

Usage:
  cloche set <key> <value>
  cloche set <key> -          (read value from stdin)

Arguments:
  <key>      The context key to write.
  <value>    The value to store, or "-" to read from stdin.

Environment:
  CLOCHE_RUN_ID        Run identifier (required).
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
  CLOCHE_ADDR    Daemon gRPC address (default: unix:///tmp/cloche.sock)

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

	"shutdown": `cloche shutdown — Shut down the daemon

Sends a shutdown signal to the Cloche daemon. Refuses to shut down if
there are active runs unless --force is specified.

Usage:
  cloche shutdown [--force|-f]

Flags:
  -f, --force   Shut down even if runs are still active.

Examples:
  cloche shutdown
  cloche shutdown --force
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
  health     Show project health summary (pass/fail counts)
  project    Show project info, config, loop state, and workflows

Workflow Info:
  workflow   List workflows or show a workflow as an ASCII-art graph
  validate   Validate project configuration and workflow definitions

Workflow Runs:
  run        Launch a workflow run in a container
  status     Check run status (state, steps, errors)
  logs       Show or stream logs for a run
  poll       Wait for one or more runs to finish (blocks until terminal)
  list       List runs for current project (or all projects)
  stop       Stop a running workflow
  delete     Delete a retained container

Orchestration:
  tasks      Show task pipeline and assignment state
  loop       Start or stop the orchestration loop

Context Store (for use inside workflow steps):
  get        Get a value from the run context store
  set        Set a value in the run context store

Daemon:
  shutdown   Shut down the Cloche daemon

Environment Variables:
  CLOCHE_ADDR          Daemon gRPC address (default: unix:///tmp/cloche.sock)
  CLOCHE_HTTP          Daemon HTTP address (for health/tasks commands)
  CLOCHE_RUN_ID        Run ID for get/set commands (set automatically in steps)
  CLOCHE_PROJECT_DIR   Project directory override for get/set commands

Examples:
  cloche init
  cloche run --workflow develop --prompt "Add user authentication"
  cloche status abc123
  cloche logs abc123 --follow
  cloche list
  cloche loop --max 2
`)
}
