# Usage

Cloche runs containerized workflows for autonomous coding agents. You write a
workflow graph in the `.cloche` DSL, point Cloche at a project directory, and it
handles execution, retry logic, and status tracking.

## Prerequisites

- Go 1.25+
- Docker
- Git
- An `ANTHROPIC_API_KEY` (for agent steps using Claude Code)

For detailed installation options (pre-built binaries, `go install`, Homebrew,
Docker-based usage), see [INSTALL.md](INSTALL.md).

## Quick Start

### 1. Build everything

```
make build
make docker-build
```

This produces `bin/cloche`, `bin/cloched`, `bin/cloche-agent`, and the
`cloche-agent:latest` Docker image.

### 2. Start the daemon

```
bin/cloched
```

The daemon listens on a Unix socket at `/tmp/cloche.sock` by default. It
manages Docker containers, tracks run state in SQLite, and handles
container cleanup. Containers are automatically removed after successful
runs unless `--keep-container` is set. Failed runs always keep their
container for debugging.

To enable the web dashboard, set `CLOCHE_HTTP` (e.g.
`CLOCHE_HTTP=:8080 bin/cloched`). The dashboard is not started unless
this variable is set.

### 3. Run a workflow

From your project directory (must be inside a git repository):

```
cd my-project
cloche run --workflow develop --prompt "Add user authentication"
```

This will:
1. Write your prompt to `.cloche/<run-id>/prompt.txt`
2. Create a Docker container and copy your entire project into it (no bind mounts)
3. Apply any override files from `.cloche/overrides/` on top
4. Start the container, which runs `cloche-agent` to walk the workflow graph
5. When the workflow finishes, the daemon extracts results to a `cloche/<run-id>` branch

### 4. Monitor progress

```
cloche status <run-id>
cloche list
```

### 5. Get the results

When the run completes, the agent's work is on a git branch:

```
git branch | grep cloche/
git diff main..cloche/<run-id>
```

To merge results into your working branch:

```
git merge cloche/<run-id>
# or cherry-pick, rebase, etc.
```

To clean up run branches:

```
git branch -D cloche/<run-id>
```

## Container Isolation Model

Cloche provides **total filesystem isolation** between the host and container:

- **Files in**: `docker cp` copies the entire project root into the container at
  `/workspace/`. No bind mounts for project files. Override files from
  `.cloche/overrides/` are then copied on top (e.g., a container-specific
  `CLAUDE.md`). The `.git/` directory is included so agents have git context.
- **Files out**: When the workflow completes, the daemon extracts results from
  the container via `docker cp` into a git worktree and commits them to a
  `cloche/<run-id>` branch.
- **Auth mounts**: `~/.claude` and `~/.claude.json` are bind-mounted read-only
  for Claude Code OAuth session reuse. `ANTHROPIC_API_KEY` is passed as an
  environment variable.
- **Network**: Containers have network access (needed for API calls).

Your project directory is never modified by the container. All changes live
on the run branch until you explicitly merge them.

## CLI Reference

### `cloche init`

Scaffold a new Cloche project in the current directory.

```
cloche init [--workflow <name>] [--image <base>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--workflow <name>` | `develop` | Workflow name. Creates `.cloche/<name>.cloche`. |
| `--image <base>` | `ubuntu:24.04` | Base Docker image for the generated Dockerfile. |

Creates `.cloche/` with a workflow file, Dockerfile, and prompt templates
(`implement.md`, `fix.md`, `update-docs.md`). Skips files that already exist.
Also adds gitignore entries for runtime state directories.

### `cloche health`

Show project health summary based on recent run results.

```
cloche health
```

Requires `CLOCHE_HTTP` to be set (e.g. `export CLOCHE_HTTP=localhost:8080`).
Fetches health data from the daemon's HTTP API (`GET /api/projects`) and
displays a table with per-project pass/fail counts:

```
PROJECT          STATUS    PASSED  FAILED  TOTAL
my-project       green     5       0       5
other-project    yellow    3       2       5
```

Status is colored (green/yellow/red) when output is a TTY.

### `cloche run`

Launch a workflow run.

```
cloche run --workflow <name> [--prompt "..."] [--keep-container]
```

| Flag | Description |
|------|-------------|
| `--workflow <name>` | Workflow name. Resolves to `.cloche/<name>.cloche` in the project directory. |
| `--prompt "..."`, `-p` | Inline prompt written to `.cloche/<run-id>/prompt.txt` and injected into agent steps. |
| `--keep-container` | Keep the Docker container even on success (default: remove on success). Failed runs always keep their container for debugging. |

The current working directory is used as the project directory. It must be
inside a git repository (Cloche needs the repo root for result extraction
via git worktrees).

When starting a run, the daemon checks whether the Docker image is up-to-date
with the project's `.cloche/Dockerfile`. If the Dockerfile has changed since
the last build (tracked via a SHA-256 label on the image), the daemon rebuilds
the image automatically before launching the container.

### `cloche status`

Check the status of a run.

```
cloche status <run-id>
```

Output includes the run state, active steps, and per-step results with timestamps.

### `cloche list`

List runs (last hour by default).

```
cloche list [--all]
```

| Flag | Description |
|------|-------------|
| `--all` | Show all runs, not just the last hour. |

Columns: run ID, workflow name, state, container ID (if running), error message (if failed).

### `cloche logs`

Show logs for a run.

```
cloche logs <run-id> [--step <name>] [--type <full|script|llm>] [--follow]
```

| Flag | Description |
|------|-------------|
| `--step <name>` | Show only logs for the specified step. |
| `--type <full\|script\|llm>` | Show only logs of the specified type (`full` = unified log, `script` = script output, `llm` = LLM conversation). |
| `--follow`, `-f` | Stream live log lines via SSE. Color-coded by type: status (blue), LLM (green), script (default). Exits when the run completes. Connects to the daemon's HTTP endpoint (see `CLOCHE_HTTP` in Client Configuration). |

Without flags, shows the unified log (`full.log`) if available, otherwise streams
step start/completion events and run results.
With `--follow`, connects to the SSE endpoint and prints log lines in real-time.

### `cloche poll`

Wait for a run to finish, printing progress updates.

```
cloche poll <run-id>
```

Polls every 2 seconds. Exits 0 on success, 1 on failure or if the container
dies unexpectedly.

### `cloche stop`

Cancel a running workflow.

```
cloche stop <run-id>
```

### `cloche delete`

Delete a retained Docker container. Accepts either a Docker container name/ID
or a Cloche run ID. If given a run ID, the daemon looks up the associated
container.

```
cloche delete <container-or-run-id>
```

### `cloche orchestrate`

Dispatch ready workflow runs for the current project. Checks the project's
issue tracker for unclaimed tasks, generates prompts, and launches runs.

```
cloche orchestrate
```

Uses the current working directory as the project directory. Prints "No ready
work found." if there are no tasks to dispatch, or "Dispatched N run(s)."
with the count.

### `cloche shutdown`

Shut down the daemon.

```
cloche shutdown
```

## Setting Up a New Project

### 1. Scaffold the project

Run `cloche init` from your project root to create the `.cloche/` directory with
default workflow, Dockerfile, and prompt templates:

```
cd my-project
cloche init
```

Or create the structure manually:

### 2. Create a workflow file

Add `.cloche/<name>.cloche`:

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

  step fix {
    prompt = file(".cloche/prompts/fix.md")
    max_attempts = "2"
    results = [success, fail, give-up]
  }

  step update-docs {
    prompt = file(".cloche/prompts/update-docs.md")
    results = [success, fail]
  }

  implement:success -> test
  implement:fail -> abort

  test:success -> update-docs
  test:fail -> fix

  fix:success -> test
  fix:fail -> abort
  fix:give-up -> abort

  update-docs:success -> done
  update-docs:fail -> done
}
```

### 3. Write prompt templates

Create `.cloche/prompts/` with markdown files:

**`.cloche/prompts/implement.md`** — Instructions for the initial implementation:
```markdown
You are working on a project. Implement the following change:

## User Request
(The user prompt is injected automatically by the agent adapter)

## Guidelines
- Follow existing project conventions
- Write tests for new functionality
- Run tests locally before declaring success
```

**`.cloche/prompts/fix.md`** — Instructions for fixing failures:
```markdown
The previous attempt had failures. Review the output logs in .cloche/output/
and fix the issues.

## Guidelines
- Read the test/lint output carefully
- Fix the root cause, not the symptoms
- Run tests again to verify your fix
```

**`.cloche/prompts/update-docs.md`** — Instructions for keeping docs in sync:
```markdown
Review the CLI source code and update usage documentation to reflect any changes.
```

### 4. Add overrides (optional)

Files in `.cloche/overrides/` are copied on top of `/workspace/` in the
container. Use this for container-specific configuration:

```
.cloche/overrides/
  CLAUDE.md              # Container-specific CLAUDE.md (replaces host version)
```

### 5. Make sure your project builds in the container

The default Dockerfile generated by `cloche init` uses `ubuntu:24.04` as its
base image (configurable via `--image`). If your project needs different
dependencies, edit `.cloche/Dockerfile`:

```dockerfile
FROM golang:1.25 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /cloche-agent ./cmd/cloche-agent

FROM ubuntu:24.04
RUN apt-get update && apt-get install -y git <your-deps> && rm -rf /var/lib/apt/lists/*
RUN npm install -g @anthropic-ai/claude-code
COPY --from=builder /cloche-agent /usr/local/bin/cloche-agent
RUN useradd -m -s /bin/bash agent
WORKDIR /workspace
RUN chown agent:agent /workspace
USER agent
```

Key requirements for the image:
- `cloche-agent` binary at `/usr/local/bin/cloche-agent`
- `git` installed (for pushing results back)
- An `agent` user (cloche wraps the command with `chown` + `su agent`)
- `/workspace` as the working directory
- Your project's build dependencies

The daemon automatically rebuilds the image when `.cloche/Dockerfile` changes
(see `cloche run` above). To build manually:
```
docker build -t my-project-agent -f .cloche/Dockerfile .
CLOCHE_IMAGE=my-project-agent:latest bin/cloched
```

### 6. Run it

```
cd my-project
cloche run --workflow develop --prompt "Add feature X"
```

## Writing Workflows

Workflow files use the `.cloche` extension and live in the `.cloche/` directory. See
[workflows.md](workflows.md) for full DSL reference.

### Step Types

**Agent steps** (has `prompt`) — Invokes a coding agent autonomously:

```
step implement {
  prompt = file(".cloche/prompts/implement.md")
  results = [success, fail]
}
```

**Script steps** (has `run`) — Runs a shell command:

```
step test {
  run = "make test 2>&1"
  results = [success, fail]
}
```

### Wiring

Connect steps with `step:result -> next_step`:

```
implement:success -> test
implement:fail -> abort
test:success -> done
test:fail -> fix
```

`done` and `abort` are built-in terminals for success and failure.

### Retry Loops

Wire failures back to earlier steps:

```
test:fail -> fix
fix:success -> test    // retry the test
fix:fail -> abort
```

### Max Attempts

Limit retries on agent steps. When exhausted, the step returns `give-up`:

```
step fix {
  prompt = file(".cloche/prompts/fix.md")
  max_attempts = "2"
  results = [success, fail, give-up]
}
```

### Parallel Branches

Wire one result to multiple targets for concurrent execution:

```
test:success -> lint
test:success -> quality
```

### Collect (Join)

Synchronize parallel branches:

```
collect all(lint:success, quality:success) -> done
```

`all` fires when every condition is met. `any` fires when at least one is.

### Agent Command Override

Agent steps use Claude Code by default. Override per-step:

```
step implement {
  prompt = "..."
  agent_command = "aider"
  results = [success, fail]
}
```

Or at the workflow level via a `container` block:

```
workflow "develop" {
  container {
    agent_command = "gemini"
  }
  ...
}
```

Or globally via `CLOCHE_AGENT_COMMAND` environment variable.

Priority (highest to lowest): step-level `agent_command`, workflow-level
`container { agent_command }`, `CLOCHE_AGENT_COMMAND` env var, default (`claude`).

### Agent Fallback Chains

Use a comma-separated list in `agent_command` to configure fallback chains. If
the first agent errors without reporting a result, the system tries the next:

```
step implement {
  prompt = "..."
  agent_command = "claude,gemini,codex"
  results = [success, fail]
}
```

Fallback rules:
- **Command not found or failed to start** — fall back to next command
- **Exit non-zero without `CLOCHE_RESULT` marker** — fall back to next command
- **Exit non-zero with `CLOCHE_RESULT` marker** — use that result (no fallback)
- **Exit 0** — use result (no fallback)
- **All commands fail to start** — step returns an error
- **Last command crashes without marker** — step returns `fail`

Known agents have default arguments (e.g., Claude gets
`-p --output-format text --dangerously-skip-permissions`). Other agents receive
the prompt on stdin with no extra flags. Override with `agent_args` at the step
or workflow level.

## Result Protocol

Steps report results by writing a marker to stdout:

```
CLOCHE_RESULT:<name>
```

For script steps, if no marker is written, the exit code determines the result:
0 = `success`, non-zero = `fail`. Agent steps follow the same convention.

## Prompt Assembly

When an agent step runs, Cloche assembles a prompt from multiple sources:

1. The step's `prompt` content (inline or from `file()`)
2. The user prompt from `.cloche/<run-id>/prompt.txt` (set via `--prompt`)
3. Validation output from `.cloche/<run-id>/output/*.log` (output from previous script steps)
4. Result selection instructions listing the step's declared results

## Project Directory Layout

```
my-project/
├── .cloche/
│   ├── develop.cloche        # Workflow definition
│   ├── Dockerfile            # Container image
│   ├── prompts/
│   │   ├── implement.md      # Prompt templates
│   │   ├── fix.md
│   │   └── update-docs.md
│   ├── overrides/            # Files copied on top of /workspace/ in container
│   │   └── CLAUDE.md         # Container-specific CLAUDE.md (optional)
│   └── <run-id>/             # Runtime state (gitignored)
│       ├── prompt.txt        # User prompt (from --prompt flag)
│       ├── output/
│       │   ├── full.log      # Unified chronological log (status + script + LLM)
│       │   ├── test.log      # Per-step script output
│       │   └── llm-impl.log  # Per-step LLM conversation output
│       ├── attempt_count/
│       │   └── fix           # Retry counter for max_attempts
│       └── history.log       # Step execution log
├── src/                      # Existing project source (untouched)
├── CLAUDE.md                 # Host CLAUDE.md
└── .git/
```

## Daemon Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_LISTEN` | `unix:///tmp/cloche.sock` | Listen address (unix socket or TCP) |
| `CLOCHE_DB` | `cloche.db` | SQLite database path |
| `CLOCHE_RUNTIME` | `docker` | `docker` (container) or `local` (subprocess, for dev) |
| `CLOCHE_IMAGE` | `cloche-agent:latest` | Default Docker image |
| `CLOCHE_HTTP` | — | HTTP address for the web dashboard. Not started unless set. |
| `CLOCHE_AGENT_PATH` | (auto-detected) | Path to `cloche-agent` binary (local runtime) |
| `CLOCHE_LLM_COMMAND` | — | Command for LLM calls — evolution and merge conflict resolution (e.g. `claude`) |
| `ANTHROPIC_API_KEY` | — | Passed into Docker containers |
| `CLOCHE_EXTRA_MOUNTS` | — | Extra bind mounts (comma-separated `host:container`) |
| `CLOCHE_EXTRA_ENV` | — | Extra env vars (comma-separated `KEY=VALUE`) |

### Client Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_ADDR` | `unix:///tmp/cloche.sock` | Daemon address (gRPC) |
| `CLOCHE_HTTP` | `localhost:8080` | Daemon HTTP address (web dashboard, `cloche logs --follow`) |

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
