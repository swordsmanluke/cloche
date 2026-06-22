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
- `resume <task-id|run-id> [step]` — Resume a failed run from a specific step (or first failed step). Bare IDs resolve to the latest attempt's failed run.
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

The daemon owns all workflow graph-walking via `DaemonExecutor`, routing steps to the
host executor or the in-container agent based on workflow location.

### cloche-agent (In-Container Runtime)

Long-lived step executor running inside the Docker container. Responsibilities:
- Connect to the daemon at `CLOCHE_ADDR` via bidirectional `AgentSession` gRPC stream
- Send `AgentReady` on startup with run/attempt IDs
- Receive `ExecuteStep` commands from the daemon
- Dispatch steps to generic (script) or prompt (LLM agent) adapters
- Stream `StepLog` lines and send `StepResult` back over the stream
- Exit gracefully on `Shutdown` command or context cancellation

The daemon owns all workflow graph-walking; the agent executes individual steps on demand.

## Communication

### CLI <-> Daemon: gRPC

- Protobuf contracts define the API (the API is the spec)
- Bidirectional streaming for `logs` (tail container output in real-time)
- Unix socket for local communication, TCP for remote

### Agent <-> Daemon: Bidirectional gRPC Stream

- The agent opens an `AgentSession` bidirectional streaming RPC to the daemon
- Daemon sends commands: `ExecuteStep`, `StepCancelled`, `Shutdown`
- Agent sends events: `AgentReady`, `StepStarted`, `StepLog`, `StepResult`
- See `docs/plans/2026-03-24-workflow-refactor-design.md` for full protocol details

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
  step code {
    prompt = file("prompts/implement.md")
    container {
      image = "cloche/agent:latest"
      network_allow = ["docs.python.org", "internal-docs.company.com"]
    }
    results = [success, fail, retry_with_feedback]
  }

  step check {
    run = "make test && make lint"
    results = [pass, fail]
  }

  step review {
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

- Step type is inferred from content: `prompt` field → agent step, `run` field → script step.
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
3. Starts the container with `cloche-agent` as entrypoint
4. `cloche-agent` connects to `cloched` via bidirectional `AgentSession` gRPC stream and sends `AgentReady`
5. `cloched` walks the workflow graph and sends `ExecuteStep` commands to the agent over the stream
6. Agent executes each step and sends back `StepResult`; log lines are streamed as `StepLog` messages
7. At step boundaries, modified files are pushed to the host via git (agent-specific branch)
8. When the workflow reaches a terminal (`done`/`abort`), `cloched` sends `Shutdown` and the container is stopped

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

## Internal Subsystems

### `internal/engine`

The workflow graph executor. Walks the parsed `Workflow` graph step by step, dispatches
each step via the `StepExecutor` interface, handles fanout (one result wired to multiple
targets) and collect (join), retry counting via `max_attempts`, and timeout wiring.

Default step timeout is 30 minutes; human/poll steps default to 72 hours. A max-step
guard (default 1000) prevents infinite loops. For resume runs, preloaded results can be
injected so completed steps replay without re-executing.

Key types:

- **`StepExecutor`** — `Execute(ctx, step) (StepResult, error)`. Implemented by the
  daemon's composite executor (host and container dispatch) and the local runtime.
- **`StatusHandler`** — Callbacks for `OnStepStart`, `OnStepComplete`, `OnStepSkipped`,
  and `OnRunComplete`. Used to drive database updates and log streaming.
- **`StepExecutorFunc`** — Adapts a plain function to `StepExecutor`.

The engine carries context values (`StepTrigger`, `*domain.Workflow`) so executors can
inspect the preceding step name/result and the workflow location without extra parameters.

### `internal/agent`

The in-container agent session manager. `Session` handles the bidirectional
`AgentSession` gRPC stream between `cloche-agent` and the daemon.

On startup, `Session.Run` connects to the daemon at `SessionConfig.Addr`, opens the
stream, and sends `AgentReady`. It then loops receiving `ExecuteStep` commands and
dispatching them to the appropriate adapter: the prompt adapter for agent steps, the
generic adapter for script steps. `StepLog` lines are streamed back as they are produced;
a `StepResult` is sent when the step finishes.

`SessionConfig` fields: `Addr` (daemon gRPC address from `CLOCHE_ADDR`), `RunID`,
`AttemptID`, `TaskID`, `WorkDir`.

### `internal/protocol`

Result protocol parsing and step history helpers used inside the container.

- **`result.go`** — `ExtractResult` scans step output for `CLOCHE_RESULT:<name>` marker
  lines. The last marker wins; all marker lines are stripped from the returned clean
  output. This is the canonical parser for the result protocol shared by the agent session
  and host executor.
- **`status.go`** — `StatusWriter` and `StatusMessage` implement the JSON-lines status
  protocol used by legacy agent adapters. Message types: `step_started`, `step_completed`,
  `run_completed`, `run_title`, `log`, `error`.
- **`history.go`** — `AppendHistory` and `AppendHistoryMarker` write step completion
  entries and workflow-level markers to `.cloche/history.log` in the container's working
  directory, providing an in-container audit trail readable by subsequent steps.

### `internal/activitylog`

Append-only activity log for attempt and step lifecycle events, persisted in the daemon's
SQLite database via the `Appender` interface (implemented by `sqlite.Store`). `Logger` is
the write side; `cloche activity` reads entries back via the same store.

**Entry schema:**

| Field | JSON key | Set for |
|-------|----------|---------|
| Timestamp | `ts` | all events |
| Kind | `kind` | all events |
| TaskID | `task_id` | all events |
| AttemptID | `attempt_id` | all events |
| WorkflowName | `workflow` | step events |
| StepName | `step` | step events |
| Result | `result` | `step_completed` |
| State | `state` | `attempt_ended` |

**Event kinds:** `attempt_started`, `attempt_ended`, `step_started`, `step_completed`.

Read-side supports `ReadOptions` with `Since` and `Until` fields for time-range filtering.
No rotation or retention policy — entries accumulate in the database for the project's
lifetime.

### `internal/runcontext`

Path helpers for per-task runtime files stored under `.cloche/runs/<taskID>/`. Provides:

- `RunDir(projectDir, taskID)` — Returns `.cloche/runs/<taskID>`.
- `PromptPath(projectDir, taskID)` — Returns `.cloche/runs/<taskID>/prompt.txt`.
- `ContextPath(projectDir, taskID)` — Returns `.cloche/runs/<taskID>/context.json`
  (retained for backward compatibility; new code uses the gRPC KV store).
- `Cleanup(projectDir, taskID)` — Removes the run directory.

The key-value context that was previously stored in `context.json` has moved to the
daemon's gRPC-backed SQLite KV store (`GetContextKey` / `SetContextKey` RPCs). The
package's own comment marks `ContextPath` and `Cleanup` as superseded. `RunDir` and
`PromptPath` remain active — they are used to locate the `prompt.txt` file written by
`cloche run --prompt` and the scratch directory exposed as `temp_file_dir` in the KV
store.

### `internal/projectcli`

Rendering helpers for `cloche project` subcommands. Contains a single function,
`WriteReposList`, which formats a slice of `*pb.Repository` as a fixed-width table
(NAME / PATH / URL columns) to an `io.Writer`. Used by `cloche project repos list`.
