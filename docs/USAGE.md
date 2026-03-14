# Cloche Usage

Cloche runs containerized workflows for autonomous coding agents. You define a
workflow graph in the `.cloche` DSL, Cloche copies your project into a Docker
container, runs the workflow steps (agent prompts and shell scripts), and extracts
results to a git branch.

## Core Concepts

- **Workflow**: A directed graph of steps connected by named-result wiring.
- **Step**: A unit of work. Type is inferred: `prompt` = agent step, `run` = script step, `workflow_name` = workflow step (host only).
- **Result**: A named outcome reported by a step (e.g. `success`, `fail`, `needs-research`). Steps declare their possible results; the engine follows wiring based on the reported result.
- **Wiring**: Maps `step:result` pairs to the next step. Separate from step definitions.
- **Terminals**: `done` (success) and `abort` (failure) are built-in terminal targets.

## Workflow DSL

Workflow files use the `.cloche` extension and live in `.cloche/`. The first step
declared is the entry point. Graphs are validated at parse time (all results wired,
no orphans, entry point exists).

### Minimal Example

```
workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test 2>&1"
    results = [success, fail]
  }

  implement:success -> test
  implement:fail -> abort
  test:success -> done
  test:fail -> abort
}
```

### Step Configuration

| Key | Type | Description |
|-----|------|-------------|
| `prompt` | string or `file("path")` | Prompt template. Makes this an agent step. |
| `run` | string | Shell command. Makes this a script step. |
| `workflow_name` | string | Container workflow to dispatch. Makes this a workflow step (host only). |
| `results` | ident list | Declared result names, e.g. `[success, fail, give-up]`. |
| `max_attempts` | string | Max retries before automatic `give-up` result, e.g. `"2"`. |
| `timeout` | string | Step timeout as Go duration, e.g. `"30m"`, `"2h"`. Default: 30m. |
| `agent_command` | string | Agent binary name(s), comma-separated for fallback chains, e.g. `"claude,gemini"`. |
| `agent_args` | string | Override default agent arguments. |
| `feedback` | string | Set to `"true"` to include `.cloche/output/*.log` content in the prompt. |
| `prompt_step` | string | For workflow steps: which preceding step's output to use as the prompt. |

A step must have exactly one of `prompt`, `run`, or `workflow_name`.

### The `file()` Function

`file("path")` reads the file at the given path relative to the working directory
(`/workspace/` in containers) at execution time, not parse time. Use it for prompt
templates:

```
prompt = file(".cloche/prompts/implement.md")
```

### The `container {}` Block

Can appear at step level or workflow level. Keys are stored with a `container.` prefix.

**Step level** — per-step container config:
```
step code {
  prompt = file(".cloche/prompts/implement.md")
  container {
    image         = "custom-agent:v2"
    memory        = "4g"
    network_allow = ["docs.python.org", "api.example.com"]
  }
  results = [success, fail]
}
```

**Workflow level** — defaults for all steps:
```
workflow "develop" {
  container {
    agent_command = "gemini"
    image         = "myregistry/myimage:v2"
    memory        = "4g"
  }
  ...
}
```

Supported keys: `image`, `memory`, `network_allow`, `agent_command`, `agent_args`.

### The `host {}` Block

Declares a workflow as a host workflow. Can appear in any `.cloche` file. Keys are stored
with a `host.` prefix. Configures agent defaults for agent steps running on the host.
An empty `host {}` block (no keys) is valid and simply marks the workflow as host-side.

```
workflow "main" {
  host {
    agent_command = "claude"
  }
  ...
}
```

Supported keys: `agent_command`, `agent_args`. Step-level `agent_command` and
`agent_args` config keys override the workflow-level `host {}` defaults.

### Wiring Syntax

Connect steps with `step:result -> next_step`:

```
implement:success -> test
implement:fail -> abort
test:success -> done
test:fail -> fix
```

### Wire Output Mappings

Extract values from a step's JSON output and inject as env vars into the target:

```
step-a:success -> step-b [ ENV_VAR = output.field, OTHER = output.list[0].name ]
```

Path expressions:

| Expression | Meaning |
|---|---|
| `output` | Raw output (full string) |
| `output.key` | JSON object field access |
| `output[N]` | JSON array index (0-based) |
| `output.a.b.c` | Deeply nested field access |
| `output.items[0].name` | Mixed field and index chaining |

If the output is valid JSON, path expressions navigate the structure. If it's plain
text, only bare `output` works.

### Retry Loops

Wire failures back to earlier steps:

```
test:fail -> fix
fix:success -> test    // retry the test
fix:fail -> abort
```

Use `max_attempts` to cap retries. When exhausted, the step returns `give-up`:

```
step fix {
  prompt = file(".cloche/prompts/fix.md")
  max_attempts = "2"
  results = [success, fail, give-up]
}
```

### Parallel Branches (Fanout)

Wire one result to multiple targets for concurrent execution:

```
test:success -> lint
test:success -> quality
```

### Collect (Join)

Synchronize parallel branches:

```
collect all(lint:success, quality:success) -> done
collect any(lint:success, quality:success) -> done
```

`all` fires when every condition is met. `any` fires when at least one is.

### Comments

Line comments with `//`:

```
// This is a comment
implement:success -> test  // inline comment
```

## Result Protocol

Steps report results by printing a marker line to stdout:

```
CLOCHE_RESULT:<name>
```

For example: `CLOCHE_RESULT:success`, `CLOCHE_RESULT:needs-research`, `CLOCHE_RESULT:give-up`.

Rules:
- The **last** `CLOCHE_RESULT:` line wins if multiple are printed.
- Marker lines are **stripped** from captured output (not passed to logs or downstream steps).
- The result name must match one of the step's declared `results`.
- For script steps with no marker: exit 0 = `success`, exit non-zero = `fail`.
- For agent steps: if exit non-zero with a marker, the marker result is used. If exit non-zero without a marker, falls back to the next agent in the fallback chain (or returns `fail` if last).

## Prompt Assembly

When an agent step runs, Cloche assembles a prompt from these sections (joined by blank lines):

1. **Step template**: The step's `prompt` content (inline string or resolved `file("path")`).
2. **User request**: Content of `.cloche/<run-id>/prompt.txt` (set via `--prompt` flag), prefixed with `## User Request`.
3. **Validation output** (opt-in): If `feedback = "true"`, reads all `.log` files from `.cloche/output/` and includes them prefixed with `## Validation Output`.
4. **Result selection**: Lists the step's declared results with instructions to print exactly one `CLOCHE_RESULT:<name>` marker.

The assembled prompt is passed to the agent command via stdin.

## Agent Command Resolution

Priority (highest to lowest):
1. Step-level `agent_command`
2. Workflow-level config block (`container { agent_command }` for container workflows, `host { agent_command }` for host workflows)
3. `CLOCHE_AGENT_COMMAND` environment variable
4. Default: `claude`

### Fallback Chains

Comma-separated commands are tried in order:

```
agent_command = "claude,gemini,codex"
```

- **Command not found / failed to start** — try next command
- **Exit non-zero without `CLOCHE_RESULT` marker** — try next command
- **Exit non-zero with `CLOCHE_RESULT` marker** — use that result (no fallback)
- **Exit 0** — use result (no fallback)
- **All commands fail to start** — step returns an error
- **Last command crashes without marker** — step returns `fail`

Known agents (e.g. `claude`) get default arguments (`-p --output-format stream-json --verbose --dangerously-skip-permissions`). Unknown agents receive the prompt on stdin with no flags. Override with `agent_args`.

### Agent Setup Guides

For detailed container setup instructions per agent:

- [How to set up Claude Code](agent-setup-claude.md) — session-based auth, no API key needed
- [How to set up Codex](agent-setup-codex.md) — API key configuration

## Workflow Locations

**Container workflows** (the default) run inside Docker via `cloche-agent`. Steps may
only be `agent` or `script` type.

**Host workflows** (declared with a `host { }` block) run on the host machine as the
daemon. Any `.cloche` file can contain host workflows. Steps may be `agent`, `script`,
or `workflow` type. The `workflow_name` step type dispatches a container workflow run
and blocks until it completes. Script steps
execute with their working directory set to the main git worktree, so host-workflow
fixes on main are available to in-flight runs even if they branched earlier.

A single file may contain **multiple named workflows**. The daemon uses up to three
host workflow phases for orchestration:

| Phase | Workflow name | Purpose |
|-------|--------------|---------|
| 1 | `list-tasks` | Discover available work. Output is JSONL (one task per line). |
| 2 | `main` | Do the work. Receives a task ID via `CLOCHE_TASK_ID` env var. |
| 3 | `finalize` | Post-main cleanup. Runs on **both** success and failure. |

Additionally, a **`release-task`** host workflow may be defined. This is not part of the
automatic orchestration loop — it is invoked on demand (e.g. from the
web dashboard) to release a stale claimed task back to `open` status. A task is
considered stale when it has `in_progress` status but no active worker is running for
it (e.g. after a failed run or daemon restart). The workflow receives `CLOCHE_TASK_ID`.

Only `main` is required. If `list-tasks` is absent, the daemon uses a single-workflow
mode. If `finalize` is absent, it is skipped.

### list-tasks Output Format

The `list-tasks` workflow's final step output is parsed as JSONL. Each line is a JSON
object:

| Field | Required | Description |
|-------|----------|-------------|
| `id` | yes | Unique task identifier |
| `status` | yes | One of `open`, `closed`, `in-progress` |
| `title` | no | Short summary |
| `description` | no | Full description |
| `metadata` | no | Arbitrary key-value pairs |

The daemon picks the first task with status `open`, runs `main` with that task's ID
set in `CLOCHE_TASK_ID`, then runs `finalize` with the outcome. Tasks are
deduplicated within a timeout window to prevent rapid reassignment of the same task.

### Host Workflow Example

A three-phase host workflow setup:

```
workflow "list-tasks" {
  step fetch-tickets {
    run     = "bash .cloche/scripts/ready-tasks.sh"
    results = [success, fail]
  }

  fetch-tickets:success -> done
  fetch-tickets:fail    -> abort
}

workflow "main" {
  host {
    agent_command = "claude"
  }

  step prepare-prompt {
    run     = "bash .cloche/scripts/prepare-prompt.sh"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  prepare-prompt:success -> develop
  prepare-prompt:fail    -> abort
  develop:success        -> done
  develop:fail           -> done
}

workflow "finalize" {
  step push-for-review {
    run     = "bash .cloche/scripts/push-for-review.sh"
    results = [success, fail]
  }

  push-for-review:success -> done
  push-for-review:fail    -> done
}
```

The `list-tasks` script writes JSONL to `$CLOCHE_STEP_OUTPUT`. The `main` workflow
receives the task ID and is responsible for claiming the ticket. The `finalize`
workflow receives `CLOCHE_MAIN_OUTCOME` and `CLOCHE_MAIN_RUN_ID` env vars to decide
what cleanup to perform.

### Host Step Environment Variables

| Variable | Description |
|----------|-------------|
| `CLOCHE_PROJECT_DIR` | Absolute path to the project directory on the host. |
| `CLOCHE_STEP_OUTPUT` | Path where this step should write its output (for output mappings). |
| `CLOCHE_PREV_OUTPUT` | Path to the output file from the immediately preceding step. |
| `CLOCHE_RUN_ID` | The run ID for this workflow execution. |
| `CLOCHE_TASK_ID` | Task ID assigned by the daemon (set for `main` and `finalize` phases). |
| `CLOCHE_MAIN_OUTCOME` | Result of the `main` workflow (`succeeded` or `failed`). Set for `finalize` phase only. |
| `CLOCHE_MAIN_RUN_ID` | Run ID of the completed `main` workflow. Set for `finalize` phase only. |
| Wire-mapped vars | Any env vars declared in wire output mappings. |

### Container Environment Variables

| Variable | Description |
|----------|-------------|
| `CLOCHE_RUN_ID` | The run ID for this workflow execution. |
| `CLOCHE_PROJECT_DIR` | Working directory (set for script steps so `cloche get`/`cloche set` work). |
| `ANTHROPIC_API_KEY` | Passed through from the host if set. |
| `CLOCHE_AGENT_COMMAND` | Overrides the default agent command inside the container. |

## Setting Up Host Workflows

Host workflows are the orchestration layer that ties everything together. When you
run `cloche init`, a minimal host workflow with just a `main` workflow is generated.
As your project grows, you can add `list-tasks` and `finalize` workflows to automate
task discovery and cleanup.

The daemon recognizes three host workflow names and runs them in order:

1. **`list-tasks`** — discover available work
2. **`main`** — execute the work
3. **`finalize`** — clean up after the work completes

A fourth optional workflow, **`release-task`**, can be defined for releasing stale
claimed tasks back to open status. It is not part of the automatic loop — it runs on
demand when triggered from the web dashboard's Release button.

These host workflows can live in any `.cloche` file(s). Only `main` is required;
`list-tasks`, `finalize`, and `release-task` are optional.

### `list-tasks` — Discovering Work

The `list-tasks` workflow runs first. Its job is to query your task tracker (issue
tracker, project board, ticket queue, etc.) and output a list of available tasks.

**When it runs:** The daemon calls `list-tasks` at the start of each orchestration
loop iteration, before launching any new `main` runs.

**Output format:** The final step must write JSONL (one JSON object per line) to
`$CLOCHE_STEP_OUTPUT`. Each line represents a task:

```json
{"id": "PROJ-42", "status": "open", "title": "Fix login bug", "description": "Users report..."}
{"id": "PROJ-43", "status": "in-progress", "title": "Add caching", "description": "..."}
{"id": "PROJ-44", "status": "open", "title": "Update docs", "description": "..."}
```

The daemon picks the first task with `status: "open"` and passes it to `main`.
Tasks that were recently assigned are deduplicated within a timeout window (default
5 minutes) to prevent the same task from being picked repeatedly.

**How to configure:** Write a script that queries your task source and outputs JSONL:

```
workflow "list-tasks" {
  step fetch-tickets {
    run     = "bash .cloche/scripts/ready-tasks.sh"
    results = [success, fail]
  }

  fetch-tickets:success -> done
  fetch-tickets:fail    -> abort
}
```

**If omitted:** The daemon falls back to single-workflow mode — it runs `main`
once without task assignment. This is fine for simple use cases where you trigger
runs manually with `cloche run`.

### `main` — Executing Work

The `main` workflow does the actual work. This is the only required host workflow
and is always generated by `cloche init`.

**When it runs:** After `list-tasks` selects a task (or immediately if `list-tasks`
is absent). The daemon sets the `CLOCHE_TASK_ID` environment variable from the
selected task.

**How to configure:** A typical `main` workflow prepares a prompt from the task data,
then dispatches a container workflow:

```
workflow "main" {
  host {
    agent_command = "claude"
  }

  step prepare-prompt {
    run     = "bash .cloche/scripts/prepare-prompt.sh"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    prompt_step   = "prepare-prompt"
    results       = [success, fail]
  }

  prepare-prompt:success -> develop
  prepare-prompt:fail    -> abort
  develop:success        -> done
  develop:fail           -> done
}
```

The `workflow_name` step type dispatches a container workflow run and blocks until
it completes. Use `prompt_step` to specify which preceding step's output should be
used as the prompt for the container run.

You can add steps before or after the container workflow — for example, a
`claim-task` step to mark the task as in-progress in your tracker, or a
`merge-to-main` step to merge the result branch.

### `finalize` — Post-Run Cleanup

The `finalize` workflow handles cleanup after `main` completes. It runs regardless
of whether `main` succeeded or failed.

**When it runs:** Immediately after `main` finishes (success or failure). The daemon
sets two additional environment variables:

- `CLOCHE_MAIN_OUTCOME` — `"succeeded"` or `"failed"`
- `CLOCHE_MAIN_RUN_ID` — the run ID of the completed `main` workflow

**How to configure:** Use the outcome to decide what cleanup to perform:

```
workflow "finalize" {
  step close-task {
    run     = "bash .cloche/scripts/close-task.sh"
    results = [success, fail]
  }

  close-task:success -> done
  close-task:fail    -> done
}
```

A typical `finalize` script checks `$CLOCHE_MAIN_OUTCOME` and acts accordingly —
closing the ticket on success, or posting a failure comment for investigation.
Wiring both results to `done` (not `abort`) ensures the finalize workflow itself
completes rather than aborting. Note that the overall task status reflects the
worst outcome of the main and finalize phases — if finalize fails, the task is
marked as failed even when main succeeded.

**If omitted:** The daemon skips the finalize phase entirely. This is fine if you
don't need automated cleanup.

### `release-task` — Releasing Stale Claims

The `release-task` workflow handles releasing tasks that are stuck in `in_progress`
status without an active worker. This happens when a run fails, the daemon restarts,
or a container is lost. The web dashboard detects these stale tasks and shows a
**Release** button next to them.

**When it runs:** On demand, when a user clicks Release in the web dashboard. The
daemon sets `CLOCHE_TASK_ID` to the task being released.

**How to configure:**

```
workflow "release-task" {
  step release-task {
    run     = "bash .cloche/scripts/release-task.sh"
    results = [success, fail]
  }

  release-task:success -> done
  release-task:fail    -> abort
}
```

A typical release script returns the ticket to `open` status and unassigns the owner
in your task tracker.

**If omitted:** The Release button will not function. Stale tasks must be manually
unclaimed in your task tracker.

### Putting It All Together

The three-phase model enables fully automated task pipelines:

1. The orchestration loop calls `list-tasks` to find open work
2. It picks an open task and launches `main` with the task context
3. `main` prepares a prompt, dispatches a container workflow, and optionally merges results
4. `finalize` closes the ticket or reports failure
5. The loop repeats, picking the next open task

Start the loop with `cloche loop` and monitor tasks with `cloche tasks`. The
`--max` flag controls how many `main` runs execute concurrently.

## Complete Example: Container Workflow with Parallel Validation

```
workflow "develop" {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "bundle exec rake test 2>&1"
    results = [success, fail]
  }

  step lint {
    run = "bundle exec rubocop 2>&1"
    results = [success, fail]
  }

  step quality {
    run = "python3 scripts/quality-check.py 2>&1"
    results = [success, fail]
  }

  step fix {
    prompt = file("prompts/fix.md")
    max_attempts = "2"
    results = [success, fail, give-up]
  }

  implement:success -> test
  implement:fail -> abort

  test:success -> lint
  test:success -> quality
  test:fail -> fix

  lint:fail -> fix
  quality:fail -> fix
  collect all(lint:success, quality:success) -> done

  fix:success -> test
  fix:fail -> abort
  fix:give-up -> abort
}
```

## Container Isolation Model

- **Files in**: `docker cp` copies the project into `/workspace/`. No bind mounts. Override files from `.cloche/overrides/` are applied on top. `.git/` is included.
- **Files out**: On completion, the daemon extracts results via `docker cp` into a git worktree and commits to a `cloche/<run-id>` branch.
- **Auth files**: `~/.claude/` and `~/.claude.json` are copied into each container at `/home/agent/` for Claude Code session reuse. Copied (not bind-mounted) so each container gets its own isolated copy.
- **Network**: Containers have network access (needed for API calls).
- **Cleanup**: Containers are removed after successful runs unless `--keep-container` is set. Failed runs always keep their container.

Your project directory is never modified by the container.

## CLI Reference

Every subcommand supports `--help` (or `-h`) to show detailed usage, flags, and
examples. Use `cloche help <command>` for the same output:

```
cloche --help              # top-level overview
cloche help run            # detailed help for "run"
cloche run --help          # same as above
```

### `cloche init`

Scaffold a new Cloche project.

```
cloche init [--workflow <name>] [--base-image <base>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--workflow <name>` | `develop` | Workflow name. Creates `.cloche/<name>.cloche`. |
| `--base-image <base>` | `cloche-base:latest` | Base Docker image for the generated Dockerfile. |

Creates `.cloche/` with workflow file, Dockerfile, `config.toml`, prompt templates,
host workflows, and prompt generation script. Skips existing files.

### `cloche run`

Launch a workflow run.

```
cloche run --workflow <name> [--prompt "..."] [--title "..."] [--issue ID] [--keep-container]
```

| Flag | Description |
|------|-------------|
| `--workflow <name>` | Workflow name. Resolves to `.cloche/<name>.cloche`. |
| `--prompt "..."`, `-p` | Inline prompt written to `.cloche/<run-id>/prompt.txt`. |
| `--title "..."` | One-line summary for status display. Auto-generated if omitted. |
| `--issue ID`, `-i` | Associate a task/issue ID with the run (shown in list output). |
| `--keep-container` | Keep container on success (failed runs always keep it). |

Must be run from inside a git repository. The daemon auto-rebuilds the Docker image
when `.cloche/Dockerfile` changes.

### `cloche status`

```
cloche status <run-id>
```

Shows run title, type (`host`/`container`), state, active steps, and per-step results.

### `cloche list`

```
cloche list [flags]
```

Lists runs for the current project directory. Pass `--all` to show runs across all projects.
Results can be filtered by state, project, issue, or limited to a fixed number.

| Flag | Description |
|------|-------------|
| `--all` | Show runs from all projects (default: current project only). |
| `--project, -p DIR` | Filter by project directory. |
| `--state, -s STATE` | Filter by run state (`pending`, `running`, `succeeded`, `failed`, `cancelled`). |
| `--limit, -n NUM` | Limit the number of results returned. |
| `--issue, -i ID` | Filter by issue/task ID. |

### `cloche logs`

```
cloche logs <run-id> [--step <name>] [--type <full|script|llm>] [-f] [-l <n>]
```

| Flag | Description |
|------|-------------|
| `--step, -s <name>` | Show only logs for the specified step. |
| `--type <full\|script\|llm>` | Log type filter. |
| `--follow, -f` | Follow mode: display existing logs then continue streaming new lines as they arrive (like `tail -f`). |
| `--limit, -l <n>` | Display only the last n lines of output. |

Flags are combinable: `cloche logs run-id -s implement -l 20 -f`

Without `-f`, displays all logs captured to date and exits (even for active runs). With `-f` on an active run, existing logs are sent first, then new output is streamed in real time via gRPC until the run completes.

### `cloche poll`

```
cloche poll <run-id> [run-id...]
```

Block until all specified runs finish. Polls every 2 seconds. Exits 0 if all runs succeeded, 1 if any failed or were cancelled.

With a single run ID, prints step-level progress. With multiple run IDs, displays a compact status summary (e.g. `id1: running`) and re-prints whenever a state changes. Use `cloche logs` for detailed output of individual runs.

### `cloche stop`

```
cloche stop <run-id>
```

### `cloche delete`

```
cloche delete <container-or-run-id>
```

Delete a retained Docker container by container ID or run ID.

### `cloche health`

```
cloche health
```

Show per-project pass/fail summary. Requires `CLOCHE_HTTP`.

### `cloche workflow`

```
cloche workflow [--project <dir>]
cloche workflow <name> [--project <dir>]
```

List all workflows or render a specific workflow as an ASCII-art graph.

| Flag | Default | Description |
|------|---------|-------------|
| `--project <dir>`, `-p` | current directory | Project directory to search for workflows. |

With no arguments, lists all workflows grouped by type (container or host). With a
workflow name, renders the workflow graph showing step boxes, wiring, and result paths.
Wires are colorized: green for `success`, red for `fail`/`failed`,
blue/yellow/orange/magenta for other results. Wires to the same destination are merged
for readability.

### `cloche validate`

Validate project configuration and workflow definitions.

```
cloche validate [--project <path>] [--workflow <name>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--project <path>` | current directory | Project directory to validate. |
| `--workflow <name>` | _(all)_ | Validate only the named workflow instead of all workflows. |

Checks performed:

- **config.toml** — parses correctly, fields are valid.
- **Workflow files** — syntax, result wiring completeness, terminal coverage (all paths reach `done`/`abort`), no orphan steps, config key validation.
- **File references** — prompt `file()` paths resolve to `.cloche/prompts/`, script `run` paths resolve to `.cloche/scripts/`.
- **Cross-file consistency** — `workflow_name` references in steps resolve to defined workflows.

Exits 0 and prints "All configuration valid." on success. Exits 1 and prints each error with file path on failure.

### `cloche project`

Show project info and config.

```
cloche project [--name <label>]
```

By default, looks up the project by the current working directory. Use `--name` to look
up a project by its registered label instead.

| Flag | Default | Description |
|------|---------|-------------|
| `--name <label>` | _(cwd lookup)_ | Look up project by label (e.g. `cloche`) instead of directory. |

Output includes: config settings (active, concurrency, stagger, dedup, stop_on_error,
evolution), orchestrator loop state (running/stopped/halted), currently active runs,
and known container and host workflow names. When the loop is halted due to
`stop_on_error`, the halt error message is displayed.

### `cloche get`

```
cloche get <key>
```

Get a value from the run context store (`.cloche/<run-id>/context.json`). Requires
the `CLOCHE_RUN_ID` environment variable. Uses `CLOCHE_PROJECT_DIR` if set, otherwise
the current working directory. Exits 1 if the key is not found.

### `cloche set`

```
cloche set <key> <value|->
```

Set a value in the run context store (`.cloche/<run-id>/context.json`). Requires
the `CLOCHE_RUN_ID` environment variable. Uses `CLOCHE_PROJECT_DIR` if set, otherwise
the current working directory. Creates the file and directories if they don't exist.
Pass `-` as the value to read from stdin (trailing newlines are trimmed).

### `cloche tasks`

```
cloche tasks [--project <dir>]
```

Show the task pipeline and assignment state for an orchestration loop. Displays
upcoming/open tasks, which tasks are assigned to which runs, and auto-assignment
state. Requires `CLOCHE_HTTP` (talks to the daemon's web API).

| Flag | Default | Description |
|------|---------|-------------|
| `--project <dir>` | current directory name | Project to query tasks for. |

### `cloche loop`

Start, stop, or resume the daemon's orchestration loop. The loop automatically picks up
tasks from the pipeline and runs them.

```
cloche loop [--max <n>]
cloche loop stop
cloche loop resume
```

| Flag | Default | Description |
|------|---------|-------------|
| `--max <n>` | config value | Maximum concurrent runs. Defaults to the value in `.cloche/config.toml`. |

`cloche loop stop` disables the loop. Running tasks are not cancelled.

`cloche loop resume` clears the halted state after a `stop_on_error` halt, allowing
the loop to resume picking up new work. See `stop_on_error` in the project config.

### `cloche --version`

Print version information for all Cloche components.

```
cloche -v
cloche --version
```

Displays the CLI version, queries the running daemon for its version (via gRPC),
and runs `docker run --rm --entrypoint cloche-agent <image> -v` to get the agent
version from the project's container image. If the daemon is unreachable or the
image doesn't exist, the corresponding version shows `<unavailable>`. Version
mismatches between components produce warnings on stderr.

The daemon and agent binaries also support standalone version output:

```
cloched -v       # prints daemon version
cloche-agent -v  # prints agent version
```

### `cloche shutdown`

```
cloche shutdown
```

## Project Layout

```
my-project/
├── .cloche/
│   ├── develop.cloche        # Container workflow
│   ├── host.cloche           # Host orchestration workflows (contain host { } blocks)
│   ├── Dockerfile            # Container image definition
│   ├── config.toml           # Project configuration
│   ├── prompts/              # Prompt templates
│   │   ├── implement.md
│   │   ├── fix.md
│   │   └── update-docs.md
│   ├── scripts/              # Host-side scripts
│   ├── overrides/            # Files copied on top of /workspace/
│   │   └── CLAUDE.md         # Container-specific CLAUDE.md (optional)
│   └── <run-id>/             # Runtime state (gitignored)
│       ├── prompt.txt        # User prompt
│       ├── context.json      # Shared key-value store (cloche get/set)
│       ├── output/
│       │   ├── full.log      # Unified log
│       │   ├── test.log      # Per-step script output
│       │   └── llm-impl.log  # Per-step LLM conversation
│       ├── attempt_count/    # Retry counters for max_attempts
│       └── history.log       # Step execution log
├── src/                      # Project source (untouched by Cloche)
└── .git/
```

## Daemon Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_LISTEN` | `unix:///tmp/cloche.sock` | Listen address |
| `CLOCHE_DB` | `cloche.db` | SQLite database path |
| `CLOCHE_RUNTIME` | `docker` | `docker` or `local` (subprocess, for dev only) |
| `CLOCHE_IMAGE` | `cloche-agent:latest` | Default Docker image |
| `CLOCHE_HTTP` | _(unset)_ | HTTP address for web dashboard. Not started unless set. |
| `CLOCHE_AGENT_PATH` | _(auto)_ | Path to `cloche-agent` binary (local runtime) |
| `CLOCHE_LLM_COMMAND` | _(unset)_ | Command for LLM calls (evolution, merge conflicts) |
| `ANTHROPIC_API_KEY` | _(unset)_ | Passed into Docker containers |
| `CLOCHE_EXTRA_MOUNTS` | _(unset)_ | Extra bind mounts (comma-separated `host:container`) |
| `CLOCHE_EXTRA_ENV` | _(unset)_ | Extra env vars (comma-separated `KEY=VALUE`) |

### Client Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_ADDR` | `unix:///tmp/cloche.sock` | Daemon gRPC address |
| `CLOCHE_HTTP` | `localhost:8080` | Daemon HTTP address |

## Dockerfile Requirements

The container image must have:
- `cloche-agent` binary at `/usr/local/bin/cloche-agent`
- `git` installed
- An `agent` user (cloche wraps commands with `chown` + `su agent`)
- `/workspace` as the working directory
- Your project's build dependencies and the agent binary (e.g. `claude`)

## Build Commands

```
make build          # Build all binaries to bin/
make test           # Run all tests
make test-short     # Run tests (skip slow ones)
make lint           # Run go vet
make proto          # Regenerate gRPC code from protobuf
make docker-build   # Build the cloche-agent Docker image
make install        # Build, install to ~/.local/bin/, restart daemon
make clean          # Remove bin/
```
