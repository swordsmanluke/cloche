# Cloche System Design

## Overview

Cloche is a system for grow-coding high quality applications. It provides containerized
environments for coding agents to operate in isolation, a workflow DSL for linking
agentic and script-driven tasks, validated code pipelines, and self-evolving tooling.

This document describes the architecture for the MVP (v0.1).

## System Components

Cloche is a Go monorepo producing three binaries:

```
cmd/
  cloche/         # CLI client (short-lived)
  cloched/        # Daemon/orchestrator (long-running)
  cloche-agent/   # In-container entrypoint (autonomous)
```

### cloche (CLI)

Short-lived process. Talks to `cloched` over gRPC (Unix socket locally, TCP remotely).

Subcommands:
- `run <workflow>` — Launch a new workflow run
- `status [run-id]` — Check run status
- `logs <run-id>` — Stream logs from a running workflow (bidirectional gRPC stream)
- `stop <run-id>` — Cancel a running workflow (signals container with SIGTERM)

Accepts prompt files or stdin input for user commands.

### cloched (Daemon)

Long-running process. Responsibilities:
- Launch and manage Docker containers for workflow runs
- Listen to status streams from running agents
- Record captures (step inputs, outputs, results, logs)
- Expose gRPC API for the CLI
- Persist state via pluggable storage (SQLite initially)

The daemon does NOT interpret workflow logic. It knows "container X is running workflow Y"
and collects status. The agent is self-directing.

### cloche-agent (In-Container Runtime)

Autonomous process running inside the Docker container. Responsibilities:
- Parse the workflow DSL (workflow files live in the codebase)
- Walk the workflow graph, executing steps and following results
- Invoke coding agents (Claude Code, custom commands) and scripts
- Stream structured status events back to `cloched`
- Run to completion (`done` or `abort`) without human intervention

The agent is told which workflow to execute at launch. It handles everything else.

## Communication

### CLI <-> Daemon: gRPC

- Protobuf contracts define the API (the API is the spec)
- Bidirectional streaming for `logs` (tail container output in real-time)
- Unix socket for local communication, TCP for remote

### Agent <-> Daemon: Structured JSON over stdout

- The agent writes JSON-lines to stdout with status updates
- `cloched` attaches to the container's stdout stream via Docker API
- Messages include: step started, step completed (with result name), logs, errors
- Kill/cancel: daemon sends SIGTERM to container, agent handles graceful shutdown

## Workflow DSL

### Vocabulary

| Concept  | Description                                                        |
|----------|--------------------------------------------------------------------|
| workflow | A named graph of steps connected by result wiring                  |
| step     | A unit of work (type: `agent` or `script`)                         |
| result   | A named outcome reported by a step (e.g. "success", "fail")       |
| wiring   | Maps a step's result to the next step                              |
| done     | Built-in terminal step indicating successful completion            |
| abort    | Built-in terminal step indicating failure (reports error and exits)|

### Syntax

```
workflow "implement-feature" {
  step code(agent) {
    prompt = file("prompts/implement.md")
    container {
      image = "cloche/agent:latest"
      network_allow = ["docs.python.org", "internal-docs.company.com"]
    }
    results = [success, fail, retry_with_feedback]
  }

  step check(script) {
    run = "make test && make lint"
    results = [pass, fail]
  }

  step review(agent) {
    prompt = file("prompts/review.md")
    input = step.code.output
    results = [approved, changes_requested]
  }

  code:success -> check
  code:fail -> abort
  code:retry_with_feedback -> code

  check:pass -> review
  check:fail -> code:retry_with_feedback

  review:approved -> done
  review:changes_requested -> code
}
```

### Key Properties

- Steps declare which results they can emit. The step decides at runtime which result to report.
- Wiring is separate from step definitions. This enables runtime injection of new steps
  (the self-evolution feature) — insert a new check between existing steps by rewiring,
  without modifying either step.
- `file()` loads external content (prompt files, scripts).
- Step inputs can reference other step outputs (`step.code.output`).
- The graph engine validates at parse time: all declared results are wired, no orphan steps,
  entry point exists.

## Hexagonal Architecture

The core domain is independent of Docker, gRPC, SQLite, or any other infrastructure.

### Domain

```
internal/domain/
  workflow.go    # Workflow, Step, Result, Graph types
  run.go         # Run, RunState, StepExecution types
  capture.go     # CapturedInput, CapturedOutput for replay
```

### Ports (interfaces)

```
internal/ports/
  store.go       # RunStore, WorkflowStore, CaptureStore
  container.go   # ContainerRuntime (start, stop, attach, logs)
  agent.go       # AgentAdapter (how to invoke an agent)
  notifier.go    # StatusNotifier (how to report progress)
```

### Adapters (implementations)

```
internal/adapters/
  docker/        # ContainerRuntime via Docker API
  sqlite/        # Store implementations via SQLite
  grpc/          # gRPC server (daemon) and client (CLI)
  agents/
    claudecode/  # AgentAdapter for Claude Code
    generic/     # AgentAdapter for arbitrary commands
```

### Key Interfaces

- **ContainerRuntime** — Start/stop containers, attach to stdout, copy files in/out.
  Docker today, Podman or Firecracker later.
- **RunStore** — Persist run state (active step, reported results, timestamps).
- **CaptureStore** — Store step inputs/outputs for replay. Git refs serve as pointers
  to file states; metadata lives in the store.
- **AgentAdapter** — Translates between Cloche's step protocol and a specific agent's
  interface. Claude Code adapter knows how to invoke `claude` CLI. Generic adapter runs
  any command.
- **StatusNotifier** — How the agent reports progress. Stdout JSON-lines initially.

## Container Model

### One Container Per Workflow Run

A container is spun up for a workflow run. All steps execute inside the same container.
File state accumulates naturally across steps (agent modifies code, then test script
runs against those modifications).

### Lifecycle

1. `cloched` pulls/builds the container image
2. Copies project files into the container at `/workspace`
3. Starts the container with `cloche-agent` as entrypoint, passing the workflow name
4. `cloche-agent` parses the workflow, walks the graph, executes steps
5. Agent streams status events to `cloched` via stdout
6. At step boundaries, modified files are pushed to the host via git (agent-specific branch)
7. On completion (`done`/`abort`), container is stopped

### File Extraction via Git

The container's git remote is configured to push to the host repository. Each run pushes
to a unique branch (e.g. `cloche/run-<id>/step-<name>`). This provides:
- Full git history of what the agent did
- Trivial diffs between input and output states
- Clean branch-per-step organization
- No bind mounts (maintains isolation)

### Security Boundaries

1. **Filesystem** — No access beyond `/workspace`. Copy-in at start, git push for extraction.
2. **Network** — `--network none` by default. If `network_allow` is specified, Cloche creates
   a custom Docker network restricting egress to allowlisted domains via DNS proxy.
3. **Environment** — Configured in Dockerfile/image. Cloche does not inject env vars.

## Capture System

Each step execution is captured for later inspection and replay.

### What Gets Captured

- Step config (prompt, type, container settings)
- Input state (git ref of what was provided to the step)
- Output state (git ref of what the step produced)
- Result reported (which result name)
- Stdout/stderr logs
- Timing metadata

### Storage

Metadata in the store (SQLite initially). Git refs serve as pointers to actual file
states — no duplication of file content.

## MVP Scope (v0.1)

### In Scope

- `cloche` CLI: `run`, `status`, `logs`, `stop`
- `cloched` daemon: container lifecycle, status collection, gRPC API, SQLite store
- `cloche-agent`: DSL parser, graph engine, agent/script execution, status reporting
- Workflow DSL: steps, results, wiring, `done`/`abort` terminals, `file()` references
- Container model: Docker, copy-in project files, git push for extraction, network allowlist
- Agent adapters: Generic (run a command) + Claude Code
- Capture system: Record step inputs/outputs/results

### Post-MVP

- Self-evolution: failure pattern analysis, automatic step injection into workflows
- Replay system: partial/full/dry replay of captured runs
- Auto-split commits into stacked branches
- Additional agent adapters
- Alternative storage backends (Postgres, DynamoDB)
- Alternative container runtimes (Podman, Firecracker)
