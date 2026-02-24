# Usage

Cloche runs containerized workflows for autonomous coding agents. You write a
workflow graph in the `.cloche` DSL, point Cloche at a project directory, and it
handles execution, retry logic, and status tracking.

## Prerequisites

- Go 1.25+
- Docker
- Git
- An `ANTHROPIC_API_KEY` (for agent steps using Claude Code)

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
manages Docker containers, tracks run state in SQLite, and cleans up
resources when runs complete.

### 3. Run a workflow

From your project directory (must be inside a git repository):

```
cd my-project
cloche run --workflow develop --prompt "Add user authentication"
```

This will:
1. Write your prompt to `.cloche/prompt.txt`
2. Start a git daemon on the host to receive results
3. Create a Docker container and copy your project files in (no bind mounts)
4. Start the container, which runs `cloche-agent` to walk the workflow graph
5. When the workflow finishes, the agent pushes results to a `cloche/<run-id>` branch

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

- **Files in**: `docker cp` copies the project directory into the container.
  No bind mounts for project files. The container gets a clean copy without
  `.git` history.
- **Files out**: The agent does `git init` + `git push` at the end of the
  workflow, pushing to a `cloche/<run-id>` branch on the host via the `git://`
  protocol.
- **Auth mounts**: `~/.claude` and `~/.claude.json` are bind-mounted read-only
  for Claude Code OAuth session reuse. `ANTHROPIC_API_KEY` is passed as an
  environment variable.
- **Network**: Containers have network access (needed for git push and API
  calls). The previous `--network none` mode is no longer used.

Your project directory is never modified by the container. All changes live
on the run branch until you explicitly merge them.

## CLI Reference

### `cloche run`

Launch a workflow run.

```
cloche run --workflow <name> [--prompt "..."]
```

| Flag | Description |
|------|-------------|
| `--workflow <name>` | Workflow name. Resolves to `<name>.cloche` in the project directory. |
| `--prompt "..."`, `-p` | Inline prompt written to `.cloche/prompt.txt` and injected into agent steps. |

The current working directory is used as the project directory. It must be
inside a git repository (Cloche needs the repo root to set up the git daemon
for result extraction).

### `cloche status`

Check the status of a run.

```
cloche status <run-id>
```

Output includes the run state, active steps, and per-step results with timestamps.

### `cloche list`

List all runs.

```
cloche list
```

Columns: run ID, workflow name, state, start time.

### `cloche stop`

Cancel a running workflow.

```
cloche stop <run-id>
```

## Setting Up a New Project

### 1. Create a workflow file

Add a `<name>.cloche` file to your project root:

```
workflow "develop" {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test 2>&1"
    results = [success, fail]
  }

  step fix {
    prompt = file("prompts/fix.md")
    max_attempts = "2"
    results = [success, fail, give-up]
  }

  implement:success -> test
  implement:fail -> abort

  test:success -> done
  test:fail -> fix

  fix:success -> test
  fix:fail -> abort
  fix:give-up -> abort
}
```

### 2. Write prompt templates

Create a `prompts/` directory with markdown files:

**`prompts/implement.md`** — Instructions for the initial implementation:
```markdown
You are working on a project. Implement the following change:

## User Request
(Contents of .cloche/prompt.txt will be injected here by the adapter)

## Guidelines
- Follow existing project conventions
- Write tests for new functionality
- Run tests locally before declaring success
```

**`prompts/fix.md`** — Instructions for fixing failures:
```markdown
The previous attempt had failures. Review the output logs in .cloche/output/
and fix the issues.

## Guidelines
- Read the test/lint output carefully
- Fix the root cause, not the symptoms
- Run tests again to verify your fix
```

### 3. Make sure your project builds in the container

The default `cloche-agent` Docker image is based on `ruby:3.3` with Node.js,
Python, and git. If your project needs different dependencies, create a custom
Dockerfile:

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

Build and set it:
```
docker build -t my-project-agent .
CLOCHE_IMAGE=my-project-agent:latest bin/cloched
```

### 4. Run it

```
cd my-project
cloche run --workflow develop --prompt "Add feature X"
```

## Writing Workflows

Workflow files use the `.cloche` extension and live in the project root. See
[workflows.md](workflows.md) for full DSL reference.

### Step Types

**Agent steps** (has `prompt`) — Invokes a coding agent autonomously:

```
step implement {
  prompt = file("prompts/implement.md")
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
  prompt = file("prompts/fix.md")
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

Or globally via `CLOCHE_AGENT_COMMAND` environment variable.

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
2. The user prompt from `.cloche/prompt.txt` (set via `--prompt`)
3. Validation output from `.cloche/output/*.log` (output from previous script steps)
4. Result selection instructions listing the step's declared results

## Project Directory Layout

```
my-project/
  develop.cloche            # Workflow file
  prompts/
    implement.md            # Prompt templates
    fix.md
  .cloche/                  # Created at runtime
    prompt.txt              # User prompt (from --prompt flag)
    output/
      test.log              # Step output logs
    attempt_count/
      fix                   # Retry counter for max_attempts
    history.log             # Step execution log
```

## Daemon Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_LISTEN` | `unix:///tmp/cloche.sock` | Listen address (unix socket or TCP) |
| `CLOCHE_DB` | `cloche.db` | SQLite database path |
| `CLOCHE_RUNTIME` | `docker` | `docker` (container) or `local` (subprocess, for dev) |
| `CLOCHE_IMAGE` | `cloche-agent:latest` | Default Docker image |
| `CLOCHE_AGENT_PATH` | (auto-detected) | Path to `cloche-agent` binary (local runtime) |
| `ANTHROPIC_API_KEY` | — | Passed into Docker containers |
| `CLOCHE_EXTRA_MOUNTS` | — | Extra bind mounts (comma-separated `host:container`) |
| `CLOCHE_EXTRA_ENV` | — | Extra env vars (comma-separated `KEY=VALUE`) |

### Client Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_ADDR` | `unix:///tmp/cloche.sock` | Daemon address |

## Build Commands

```
make build          # Build all binaries to bin/
make test           # Run all tests
make test-short     # Run tests (skip slow ones)
make lint           # Run go vet
make proto          # Regenerate gRPC code from protobuf
make docker-build   # Build the cloche-agent Docker image
make clean          # Remove bin/
```
