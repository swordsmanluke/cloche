# Usage

Cloche runs containerized workflows for autonomous coding agents. You write a
workflow graph in the `.cloche` DSL, point Cloche at a project directory, and it
handles execution, retry logic, and status tracking.

## Quick Start

Build the binaries:

```
make build
```

This produces `bin/cloche`, `bin/cloched`, and `bin/cloche-agent`.

Start the daemon:

```
bin/cloched
```

Run a workflow:

```
bin/cloche run --workflow build --prompt "Add a login page"
```

Check status:

```
bin/cloche status <run-id>
```

## Architecture

Three binaries work together:

- **`cloche`** — CLI client. Sends commands to the daemon over gRPC.
- **`cloched`** — Daemon. Manages containers, persists state, collects status.
- **`cloche-agent`** — In-container entrypoint. Parses the workflow, walks the
  graph, executes steps, streams status back.

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

The current working directory is used as the project directory.

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

## Writing Workflows

Workflow files use the `.cloche` extension and live in the project root. See
[workflows.md](workflows.md) for full DSL reference.

### Minimal Example

```
workflow "hello" {
  step build {
    run = "make build"
    results = [success, fail]
  }

  step test {
    run = "make test"
    results = [success, fail]
  }

  build:success -> test
  build:fail    -> abort
  test:success  -> done
  test:fail     -> abort
}
```

The first step defined is the entry point. `done` and `abort` are built-in
terminals for success and failure.

### Agent Steps

Use `prompt` to invoke a coding agent:

```
step implement {
  prompt = file("prompts/implement.md")
  results = [success, fail]
}
```

The `file()` function loads prompt content from a file relative to the project
directory. Inline prompts work too:

```
step implement {
  prompt = "Implement the feature described in the user request."
  results = [success, fail]
}
```

Agent steps run Claude Code by default. Override with `agent_command`:

```
step implement {
  prompt = "..."
  agent_command = "aider"
  results = [success, fail]
}
```

### Script Steps

Use `run` to execute shell commands:

```
step test {
  run = "bundle exec rake test 2>&1"
  results = [success, fail]
}
```

Exit code 0 maps to `success`, non-zero maps to `fail`, unless the script
writes an explicit `CLOCHE_RESULT:<name>` marker to stdout.

### Retry Loops

Wire a failure back to an earlier step:

```
step build {
  run = "make build"
  results = [success, fail]
}

step test {
  run = "make test"
  results = [success, fail]
}

build:success -> test
test:success  -> done
test:fail     -> build  // retry from build
```

### Max Attempts

Limit retries on agent steps with `max_attempts`. When exhausted, the step
returns `give-up` without invoking the agent:

```
step fix {
  prompt = file("prompts/fix.md")
  max_attempts = "2"
  results = [success, fail, give-up]
}

fix:success -> test
fix:fail    -> abort
fix:give-up -> abort
```

### Parallel Branches (Fanout)

Wire a single result to multiple targets:

```
code:success -> test
code:success -> lint
```

Both `test` and `lint` run concurrently.

### Collect (Join)

Synchronize parallel branches with `collect`:

```
collect all(test:success, lint:success) -> merge
```

`all` fires when every condition is met. `any` fires when at least one is:

```
collect any(test:success, lint:success) -> next
```

### Full Parallel Example

```
workflow "validate" {
  step code {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test"
    results = [success, fail]
  }

  step lint {
    run = "make lint"
    results = [success, fail]
  }

  step merge {
    run = "echo 'All checks passed'"
    results = [success, fail]
  }

  code:success -> test
  code:success -> lint
  code:fail    -> abort
  test:fail    -> abort
  lint:fail    -> abort
  collect all(test:success, lint:success) -> merge
  merge:success -> done
  merge:fail    -> abort
}
```

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
  build.cloche              # Workflow file
  prompts/
    implement.md            # Prompt templates
    fix.md
  .cloche/                  # Created at runtime
    prompt.txt              # User prompt (from --prompt flag)
    output/
      test.log              # Step output logs
    attempt_count/
      fix                   # Retry counter for max_attempts
```

## Daemon Configuration

The daemon is configured with environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_LISTEN` | `unix:///tmp/cloche.sock` | Listen address (unix socket or TCP) |
| `CLOCHE_DB` | `cloche.db` | SQLite database path |
| `CLOCHE_RUNTIME` | `local` | `local` (subprocess) or `docker` |
| `CLOCHE_IMAGE` | `cloche-agent:latest` | Default Docker image |
| `CLOCHE_AGENT_PATH` | (auto-detected) | Path to `cloche-agent` binary (local runtime) |
| `ANTHROPIC_API_KEY` | — | Passed into Docker containers |

### Runtime Modes

**Local** (`CLOCHE_RUNTIME=local`) — Runs `cloche-agent` directly as a
subprocess. No Docker required. Good for development.

**Docker** (`CLOCHE_RUNTIME=docker`) — Runs workflows inside Docker containers
with network isolation. One container per workflow run; all steps share the same
filesystem.

Build the container image:

```
make docker-build
```

### Client Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_ADDR` | `unix:///tmp/cloche.sock` | Daemon address |

## Agent Configuration

`cloche-agent` is the in-container entrypoint:

```
cloche-agent <workflow-file>
```

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_AGENT_COMMAND` | `claude` | LLM command for agent steps |

## Examples

### hello-world

A script-only workflow with a retry loop:

```
cd examples/hello-world
cloche run --workflow build
```

### ruby-calculator

A mixed agent + script workflow. An agent implements code, tests validate it,
then lint and code quality checks run concurrently. Failures route to an agent
fix step with retry limits:

```
cd examples/ruby-calculator
cloche run --workflow develop --prompt "Create a Calculator class with add, subtract, multiply, divide"
```

The workflow graph:

```
implement → test → lint ──────┐
                └→ quality ───┤ collect all → done
                              │
           ┌──── fix ←────────┘ (on any failure)
           └→ test (retry loop, max 2 fix attempts)
```

The `quality` step runs [speedometer](https://github.com/your-org/speedometer)
to score the git diff for code quality signals. Set `SPEEDOMETER_HOME` to point
at your speedometer checkout (defaults to `~/projects/speedometer`). Requires
Python 3.10+ and `pyyaml`.

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
