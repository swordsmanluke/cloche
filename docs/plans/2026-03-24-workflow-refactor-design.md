# Workflow System Refactor Design

**Date:** 2026-03-24
**Status:** Design

## Problem

The orchestration loop hardcodes a three-phase pipeline (list-tasks → main →
finalize) with the finalize phase baked into the daemon's `createPhaseLoop`. This
makes the workflow system implicit and rigid. Container workflows cannot trigger
host-side actions, limiting what autonomous agents can accomplish without manual
orchestration. And each `RunWorkflow` call creates a fresh container, preventing
workflows from sharing state across steps within a single attempt.

## Solution

Simplify to a two-phase loop (list-tasks → main) where main owns end-to-end
execution including any finalization steps. Centralize all workflow graph-walking
in the daemon, turning `cloche-agent` from an autonomous workflow executor into a
lightweight step executor that receives commands over a bidirectional gRPC stream.
Introduce attempt-scoped container reuse so workflows can share filesystem state.
Enable bidirectional dispatch so container steps can trigger host workflows and
vice versa.

## Design Details

### Two-Phase Orchestration Loop

The loop becomes:

```
list-tasks → main → (done)
```

The `finalize` workflow is removed as a blessed phase. Projects that need
finalization steps add them directly to their `main` workflow. The `FinalizeFunc`
field on `host.Loop`, the `finalizeFn` construction in `createPhaseLoop`, and the
`CLOCHE_MAIN_OUTCOME` / `CLOCHE_MAIN_RUN_ID` environment variable injection are
all removed.

**Files affected:**
- `internal/host/loop.go` — Remove `FinalizeFunc`, simplify `NewPhaseLoop` to
  accept only `ListTasksFunc` and `MainFunc`, remove finalize logic from
  `runPhased()`
- `internal/adapters/grpc/server.go:createPhaseLoop()` — Remove finalize
  construction, `SkipCleanup` logic, `hasFinalize` check
- `internal/host/loop_test.go` — Remove finalize-related tests, add tests for
  two-phase behavior

**Not a concern:** backward compatibility. No external users exist yet; existing
`.cloche/host.cloche` files are refactored to fold finalize steps into main.

### Daemon as Single Orchestrator

Today, two separate graph-walkers exist:

1. **Daemon-side** (`internal/host/executor.go`): Walks host workflow graphs,
   dispatches container workflows as opaque units
2. **Agent-side** (`internal/engine/engine.go`): Walks container workflow graphs
   autonomously inside the container

These merge into **one orchestrator on the daemon side**. The daemon walks all
workflow graphs — host and container — using the existing `engine.Engine`. When a
step targets a container, the daemon sends an "execute step" command to the
agent over gRPC. When a step targets the host, the daemon executes it locally
(as the host executor does today).

This means:

- The daemon parses all `.cloche` workflow files (host and container)
- The daemon resolves workflow references by name (project-unique)
- The daemon tracks step state for all workflows in one place
- The daemon handles wiring, fanout, and collect conditions for all workflows

**`internal/engine/engine.go`** remains largely unchanged. The `StepExecutor`
interface becomes the dispatch point: the daemon provides a `StepExecutor`
implementation that routes steps to the right executor based on the workflow's
location.

```go
// DaemonExecutor routes steps to host or container executors.
type DaemonExecutor struct {
    hostExec      *host.Executor       // runs scripts/agents on host
    containers    *ContainerPool       // manages agent sessions
    attemptID     string
    projectDir    string
}

func (d *DaemonExecutor) Execute(ctx context.Context, step *domain.Step) (domain.StepResult, error) {
    wf := engine.WorkflowFromContext(ctx)
    if wf.Location == domain.LocationHost {
        return d.hostExec.Execute(ctx, step)
    }
    // Container step: send command to agent, await result
    session := d.containers.SessionFor(ctx, wf)
    return session.ExecuteStep(ctx, step)
}
```

**Files affected:**
- `internal/engine/engine.go` — Add context plumbing to carry workflow reference
  into step execution. The engine itself is location-agnostic.
- `internal/host/executor.go` — Remains as the host-side step executor (scripts,
  local agents). Remove the `executeWorkflow` method since workflow dispatch is
  now handled by the daemon orchestrator.
- `internal/agent/runner.go` — Gutted. No longer walks graphs. Becomes the
  agent-side gRPC session handler (see Agent Protocol below).
- `cmd/cloche-agent/main.go` — Simplified to: connect to daemon, start session,
  execute steps on demand.

### Agent Protocol: Bidirectional gRPC Stream

`cloche-agent` becomes a long-lived process that connects to the daemon via a
bidirectional streaming RPC. The daemon sends commands; the agent sends events.

```protobuf
service ClocheService {
  // Existing RPCs ...

  // AgentSession is a bidirectional stream between the daemon and an in-container
  // agent. The agent connects after container startup; the daemon sends step
  // execution commands; the agent sends back status events and results.
  rpc AgentSession(stream AgentMessage) returns (stream DaemonMessage);
}

message AgentMessage {
  oneof payload {
    AgentReady       ready = 1;         // agent is up, ready for commands
    StepResult       step_result = 2;   // step completed with result
    StepLog          step_log = 3;      // real-time log line from step
    StepStarted      step_started = 4;  // step execution began
    HostWorkflowRequest host_request = 5; // agent requests host workflow
  }
}

message DaemonMessage {
  oneof payload {
    ExecuteStep      execute_step = 1;  // run this step
    StepCancelled    step_cancelled = 2; // cancel in-progress step
    HostWorkflowResult host_result = 3; // result of requested host workflow
    Shutdown         shutdown = 4;       // graceful shutdown
  }
}

message AgentReady {
  string run_id = 1;
  string attempt_id = 2;
}

message ExecuteStep {
  string step_name = 1;
  string step_type = 2;           // "agent", "script"
  map<string, string> config = 3; // prompt, run, agent_command, etc.
  map<string, string> env = 4;    // output-mapped env vars from wiring
  string request_id = 5;          // correlates with StepResult
  bool resume = 6;                // continue existing conversation rather than starting fresh
}

message StepResult {
  string request_id = 1;
  string result = 2;              // "success", "fail", custom result names
  string output = 3;              // step output content (for output mappings)
  TokenUsage token_usage = 4;     // optional
}

message StepLog {
  string step_name = 1;
  string line = 2;
  int64 timestamp = 3;
}

message StepStarted {
  string request_id = 1;
  string step_name = 2;
}

message HostWorkflowRequest {
  string request_id = 1;          // agent-generated, correlates with HostWorkflowResult
  string workflow_name = 2;
  map<string, string> env = 3;    // env vars to pass to host workflow
}

message HostWorkflowResult {
  string request_id = 1;
  string result = 2;              // "success", "fail"
  string run_id = 3;
}

message StepCancelled {
  string request_id = 1;
}

message Shutdown {}
```

**Agent lifecycle:**

1. Container starts, `cloche-agent` launches
2. Agent dials daemon at `CLOCHE_ADDR`, opens `AgentSession` stream
3. Agent sends `AgentReady` with run/attempt IDs
4. Daemon sends `ExecuteStep` commands as the workflow graph progresses
5. Agent executes each step (script or LLM agent), streams `StepLog` lines and
   sends `StepResult` on completion
6. When the daemon is done (workflow reached `done` or `abort`), it sends
   `Shutdown`
7. Agent closes stream, process exits

**Container → host workflow dispatch:**

When a container workflow contains a `workflow_name` step that resolves to a host
workflow, the daemon's orchestrator detects this during graph-walking and executes
the host workflow directly (since the daemon owns both sides). No agent
involvement needed for the dispatch itself.

However, a container-side step may also programmatically request a host workflow
(e.g., an agent step decides at runtime it needs host-side file access). For this
case, the agent sends `HostWorkflowRequest` over the stream. The daemon runs the
host workflow synchronously and returns `HostWorkflowResult`. The agent blocks the
step until the result arrives.

**Files affected:**
- `api/proto/cloche/v1/cloche.proto` — Add `AgentSession` RPC and message types
- `cmd/cloche-agent/main.go` — Rewrite: connect, open stream, dispatch steps
- `internal/agent/runner.go` — Replace graph-walking with step execution loop
- `internal/agent/session.go` (new) — Agent-side session: receives commands,
  dispatches to adapters, sends results
- `internal/adapters/grpc/server.go` — Add `AgentSession` handler, integrate with
  container pool and daemon orchestrator

### stdout for Raw Output

Today, `cloche-agent` writes JSON status messages to stdout and the daemon parses
them via `docker logs -f`. With the bidi stream, structured status (step events,
log lines) goes over gRPC. Stdout becomes raw debug output only.

The daemon's `trackRun` method changes from parsing JSON status messages off
`docker logs` to receiving events over the `AgentSession` stream. The daemon still
captures `docker logs` output for debugging (written to `container.log`) but no
longer parses it for status.

**Files affected:**
- `internal/adapters/grpc/server.go:trackRun()` — Rewrite: status comes from
  gRPC stream, not docker logs. Still capture raw docker logs to file.
- `internal/protocol/status.go` — `StatusWriter` and JSON-line format become
  internal to raw logging, no longer the daemon communication channel.

### Container Lifecycle and Reuse

**Attempt-scoped containers:**

Containers are scoped to an attempt, not a workflow run. A container starts when
the first container-side step in an attempt needs one, and lives until the attempt
ends (success, failure, or cancellation).

Default behavior: all container-side workflows in an attempt share one container
unless explicitly separated via the `id` field.

**Container ID field:**

The `container {}` block gains an `id` field:

```
workflow "develop" {
  container {
    id = "dev"
    image = "cloche-agent:latest"
    network_allow = "github.com"
  }

  step "implement" { ... }
}

workflow "test" {
  container {
    id = "dev"  // reuses the "develop" container — no other config needed
  }

  step "run-tests" { ... }
}
```

**Validation rules for `container { id = "..." }`:**

When the same `id` appears in multiple workflows:
- **(a)** All blocks share the exact same configuration, OR
- **(b)** Exactly one block has full configuration; all others define only `id`, OR
- **(c)** All blocks define only `id` (inherits from the attempt default)

`cloche validate` enforces these rules across all `.cloche` files in a project.

**Default container ID:**

When no `id` is specified, the container uses the attempt's default container. All
container workflows without an explicit `id` share one container per attempt. This
is equivalent to an implicit `id = "_default"`.

**Container pool:**

A new `ContainerPool` manages container lifecycle per attempt:

```go
type ContainerPool struct {
    mu               sync.Mutex
    runtime          ports.ContainerRuntime
    attempts         map[string]*poolEntry  // key: attemptID
    containerAttempt map[string]string      // key: containerID → attemptID
}

// ContainerSession holds the container ID for a running agent container.
type ContainerSession struct {
    ContainerID string
}

func (p *ContainerPool) SessionFor(ctx context.Context, attemptID string, cfg ports.ContainerConfig) (*ContainerSession, error)
func (p *ContainerPool) NotifyReady(containerID string)
func (p *ContainerPool) CleanupAttempt(ctx context.Context, attemptID string, keepContainer, runFailed, aborted bool) error
```

`SessionFor` returns an existing session if the container is already running, or
creates a new container and blocks until the in-container agent sends `AgentReady`
(signalled via `NotifyReady`). Subsequent calls with the same `attemptID` return
the existing session without starting another container.

`NotifyReady` is called by the gRPC server when an agent sends `AgentReady` over
its session stream. It unblocks any `SessionFor` waiting on that container.

`CleanupAttempt` is called when an attempt ends. Containers are stopped and
removed unless any of the following is true: `keepContainer` (the `--keep-container`
CLI flag), `runFailed` (the attempt result was failed/cancelled), or `aborted`
(the attempt was aborted mid-run). In the keep case, containers are left running
for debugging.

**Files affected:**
- `internal/dsl/parser.go` — Parse `id` field in `container {}` block
- `internal/dsl/validate.go` or `domain/workflow.go` — Cross-workflow container ID
  consistency validation
- `internal/adapters/docker/runtime.go` — Adjust to support long-lived containers
  (no longer exit after workflow; agent stays alive until `Shutdown`)
- `internal/adapters/docker/pool.go` (new) — `ContainerPool` implementation
- `internal/adapters/grpc/server.go` — Wire pool into orchestrator, cleanup on
  attempt completion

### Workflow Names as Project-Unique Identifiers

Workflow names become project-unique identifiers that implicitly resolve to their
declared location (host or container). A `workflow_name = "develop"` step in any
workflow — host or container — triggers the `develop` workflow wherever it is
defined.

This means:
- No two workflows in a project may share the same name (enforced by validation)
- The daemon resolves workflow names from all parsed `.cloche` files
- `ValidateLocation` relaxes: `workflow_name` steps are allowed in container
  workflows (the restriction today is removed)

**Dispatch logic in the daemon orchestrator:**

When the engine encounters a `workflow_name` step:
1. Look up the target workflow by name across all `.cloche` files
2. If it's a host workflow → execute via host executor
3. If it's a container workflow → execute via container pool (create/reuse
   container as needed)
4. Block until the sub-workflow reaches a terminal state
5. Map the outcome to a step result

**Files affected:**
- `internal/domain/workflow.go:ValidateLocation()` — Remove the container
  restriction on `workflow_name` steps
- `internal/host/runner.go:FindHostWorkflows()` → generalize to
  `FindAllWorkflows()` returning all workflows keyed by name
- `internal/dsl/parser.go` — No change needed; `workflow_name` is already a
  recognized step config key
- Validation: add a cross-file uniqueness check for workflow names

### DSL Changes Summary

**New syntax:**

```
container {
  id = "dev"              # NEW: container identity for reuse
  image = "..."
  agent_command = "..."
  agent_args = "..."
  network_allow = "..."
}
```

**Removed constraints:**
- `workflow_name` steps are now allowed in container workflows (previously
  host-only)

**No other DSL changes.** The `host {}` and `container {}` blocks, step types,
wiring syntax, agent declarations, and output mappings all remain the same.

### Resume Across Attempts

When an attempt fails and the user runs `cloche resume`, the system creates a new
attempt but preserves container state from the failed one. This is critical for
LLM conversation resume — `claude`'s conversation history lives on the container
filesystem, and re-running a failed prompt step from scratch wastes significant
time and tokens.

**Resume flow:**

1. User runs `cloche resume <run-id> --from <step>` (or the daemon triggers
   resume internally)
2. Daemon creates a new attempt record linked to the same task
3. For each container used in the failed attempt, the daemon commits the
   container's filesystem as a Docker image (`docker commit`)
4. New containers for the new attempt start from these committed images, keyed
   by container ID — so `id = "dev"` in the failed attempt maps to the same
   `id = "dev"` in the new attempt
5. The daemon walks the workflow graph, pre-loading completed step results from
   the failed attempt's records (stored in the database, not history.log)
6. At the resume step, the daemon sends `ExecuteStep` with a `resume = true`
   flag. The agent recognizes this and tells the prompt adapter to continue
   the existing conversation rather than starting fresh
7. Graph execution continues normally from there

**What changes from today:**

- Resume logic moves entirely to the daemon. The agent no longer reads
  `history.log` or manages preloaded results — it just executes steps it
  receives.
- The daemon stores completed step results in the database (already does this
  via `Run.RecordStepComplete`), so it has the data to pre-load the engine.
- `history.log` inside the container becomes purely informational (debug aid),
  not a resume mechanism.
- The `ExecuteStep` message gains a `resume` boolean field so the agent knows
  to continue a conversation rather than start one.

**Container image management:**

Committed images follow the naming convention
`cloche-resume:<attemptID>-<containerID>`. The daemon cleans up committed images
when:
- The new attempt succeeds (old images no longer needed)
- The user explicitly deletes the run

Images are kept on failure so the user can resume again.

**Files affected:**
- `internal/adapters/docker/runtime.go` — Add `CommitContainer(containerID) (imageRef, error)` method
- `internal/adapters/docker/pool.go` — `ContainerPool` gains `CommitForResume`
  and `StartFromImage` methods
- `internal/adapters/grpc/server.go` — Resume handler creates new attempt,
  commits containers, starts new containers from images, re-walks graph
- `api/proto/cloche/v1/cloche.proto` — Add `resume` field to `ExecuteStep`

### Error Handling

**Agent session disconnect:** If the agent process crashes or the gRPC stream
breaks, the daemon marks all in-flight steps as failed. The container is kept for
debugging (same as current abort behavior). The attempt records the failure.

**Container startup failure:** If a container fails to start or the agent doesn't
send `AgentReady` within a timeout (30s), the step that triggered the container is
failed. The daemon continues walking the graph (which may wire to a retry or
abort).

**Host workflow failure during container dispatch:** If a container step triggers a
host workflow that fails, the `HostWorkflowResult` carries the failure. The agent
step reports it as a step failure, and the daemon's graph-walker follows the
failure wire.

**Attempt cleanup on failure:** Same as today — containers are kept on failure for
debugging. `--keep-container` flag and `abort` terminal both preserve containers.
Success cleans up.

### Migration Path

This is a clean break. Existing projects need to:

1. **Remove `finalize` workflow** — fold its steps into `main`
2. **Update wiring** — steps that were in finalize (e.g., version bump, task
   close) become late-stage steps in main, wired after the develop/merge steps
3. **Add `container { id = "..." }`** if they want explicit container
   sharing/separation (optional — default sharing works for most cases)
4. **Remove `release-task` as a blessed workflow** — it can remain as a named
   workflow callable from main

## Alternatives Considered

**Keep the agent as an autonomous graph-walker, add host-call RPC:**
Simpler change — just add a `RunHostWorkflow` RPC that the agent can call. But
this means two graph-walkers with duplicated logic, two sources of truth for
step state, and more complex debugging. Centralizing in the daemon is cleaner
and enables future features (cross-workflow parallelism, centralized retry
policies) without agent changes.

**Container reuse by configuration hash:**
Instead of an explicit `id` field, automatically reuse containers when their
configuration matches. Rejected because implicit behavior is harder to reason
about and debug. An explicit `id` makes sharing intentional and visible.

**Keep finalize as an optional blessed phase:**
Could maintain backward compat by making finalize optional (already is) and just
adding the new capabilities. Rejected because the whole point is simplification —
keeping an implicit phase defeats the goal of making workflows explicit.

**Fresh prompt on resume (no conversation continuity):**
On cross-attempt resume, start the failed LLM step from scratch with a fresh
prompt instead of continuing the conversation. Simpler — no image commits, no
filesystem preservation. Rejected because LLM work is the most expensive part of
a run; re-doing a 30-minute coding session because of a transient failure in a
later step wastes significant time and tokens. Commit + reuse image preserves
conversation state at the cost of disk space and some plumbing.

## Resolved Questions

1. **KV store access** — `clo get/set` keeps its separate unary gRPC RPCs. It's
   used in shell scripts and should remain self-contained. The bidi stream is
   only for daemon↔agent orchestration, not for scripting tools.

2. **Resume semantics** — Resolved: commit + reuse image (see Resume section in
   Design Details above). Cross-attempt conversation resume is worth the extra
   plumbing because LLM work is the expensive part.

3. **Step output extraction** — Small outputs (result markers, status, log lines
   under 1KB) go over the gRPC stream as part of `StepResult.output` and
   `StepLog`. Large outputs (step log files, generated code, artifacts) use
   `docker cp` as today. The daemon eagerly extracts via `docker cp` after each
   `StepResult` for any output files the step wrote to `.cloche/output/`.
