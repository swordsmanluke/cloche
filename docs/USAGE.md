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

Can appear at workflow level in host workflows (`.cloche/host.cloche`). Keys are stored
with a `host.` prefix. Configures agent defaults for agent steps running on the host.

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

## Workflow Locations

**Container workflows** (`.cloche/*.cloche` except `host.cloche`) run inside Docker
via `cloche-agent`. Steps may only be `agent` or `script` type.

**Host workflow** (`.cloche/host.cloche`) runs on the host machine as the daemon.
Steps may be `agent`, `script`, or `workflow` type. The `workflow_name` step type
dispatches a container workflow run and blocks until it completes. Script steps
execute with their working directory set to the main git worktree, so host-workflow
fixes on main are available to in-flight runs even if they branched earlier.

A single `host.cloche` file may contain **multiple named workflows**. The daemon
uses up to three workflow phases for orchestration:

| Phase | Workflow name | Purpose |
|-------|--------------|---------|
| 1 | `list-tasks` | Discover available work. Output is JSONL (one task per line). |
| 2 | `main` | Do the work. Receives a task ID via `CLOCHE_TASK_ID` env var. |
| 3 | `finalize` | Post-main cleanup. Runs on **both** success and failure. |

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

A three-phase `host.cloche`:

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
| `ANTHROPIC_API_KEY` | Passed through from the host if set. |
| `CLOCHE_AGENT_COMMAND` | Overrides the default agent command inside the container. |

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
- **Auth mounts**: `~/.claude` and `~/.claude.json` are bind-mounted read-only for Claude Code session reuse.
- **Network**: Containers have network access (needed for API calls).
- **Cleanup**: Containers are removed after successful runs unless `--keep-container` is set. Failed runs always keep their container.

Your project directory is never modified by the container.

## CLI Reference

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
host workflow (`host.cloche`), and prompt generation script. Skips existing files.

### `cloche run`

Launch a workflow run.

```
cloche run --workflow <name> [--prompt "..."] [--title "..."] [--keep-container]
```

| Flag | Description |
|------|-------------|
| `--workflow <name>` | Workflow name. Resolves to `.cloche/<name>.cloche`. |
| `--prompt "..."`, `-p` | Inline prompt written to `.cloche/<run-id>/prompt.txt`. |
| `--title "..."` | One-line summary for status display. Auto-generated if omitted. |
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
cloche list [--all]
```

Lists runs for the current project directory. Pass `--all` to show runs across all projects.

### `cloche logs`

```
cloche logs <run-id> [--step <name>] [--type <full|script|llm>] [-f]
```

| Flag | Description |
|------|-------------|
| `--step <name>` | Show only logs for the specified step. |
| `--type <full\|script\|llm>` | Log type filter. |
| `-f` | Follow mode: display existing logs then continue streaming new lines as they arrive (like `tail -f`). |

Without `-f`, displays all logs captured to date and exits (even for active runs). With `-f` on an active run, existing logs are sent first, then new output is streamed in real time via gRPC until the run completes.

### `cloche poll`

```
cloche poll <run-id>
```

Block until the run finishes. Polls every 2 seconds. Exits 0 on success, 1 on failure.

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

### `cloche get`

```
cloche get <key>
```

Get a value from the run context store (`.cloche/<run-id>/context.json`). Requires
the `CLOCHE_RUN_ID` environment variable. Uses `CLOCHE_PROJECT_DIR` if set, otherwise
the current working directory. Exits 1 if the key is not found.

### `cloche set`

```
cloche set <key> <value>
```

Set a value in the run context store (`.cloche/<run-id>/context.json`). Requires
the `CLOCHE_RUN_ID` environment variable. Uses `CLOCHE_PROJECT_DIR` if set, otherwise
the current working directory. Creates the file and directories if they don't exist.

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

### `cloche shutdown`

```
cloche shutdown
```

## Project Layout

```
my-project/
├── .cloche/
│   ├── develop.cloche        # Container workflow
│   ├── host.cloche           # Host orchestration workflow
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
