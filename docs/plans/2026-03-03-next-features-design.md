# Next Features Design

Date: 2026-03-03

This document captures the design for the five features listed in PROJECT.md:
Orchestrator, Indicator Lights, Web Dashboard, Log Contents, and Container Deletion.

## 1. Orchestrator

**Goal:** Automate work dispatch — pull ready tasks from a task tracker, generate
prompts via LLM, kick off workflow runs, and manage the merge queue for landing
completed work.

**Location:** Built into `cloched` (no new binary).

**Components:**

### TaskTracker Port

New interface `ports.TaskTracker`:

```go
type Task struct {
    ID          string
    Title       string
    Description string
    Labels      []string
    Priority    int
}

type TaskTracker interface {
    ListReady(ctx context.Context, project string) ([]Task, error)
    Claim(ctx context.Context, taskID string) error
    Complete(ctx context.Context, taskID string) error
    Fail(ctx context.Context, taskID string) error
}
```

First adapter: Beads. Future adapters: Jira, Linear, GitHub Issues.

Tasks returned by `ListReady` are ordered by priority (highest first).

### PromptGenerator

Takes a `Task` and project context, calls the project's configured LLM to generate
a prompt suitable for the workflow. The generated prompt is passed to the workflow run
as the `--prompt` argument.

### Orchestration Loop

Event-driven, triggers on:
1. Daemon startup — check all projects for ready work.
2. Run completion — after a run finishes, check the completed run's project for more work.
3. `cloche loop` CLI command — manual trigger, checks all projects.

On trigger:
1. For each registered project with orchestration enabled:
2. Query task tracker for ready work (priority-ordered).
3. Check in-flight run count against per-project concurrency limit.
4. For each available slot: claim the highest-priority task, generate prompt, dispatch run.

### Configuration

Per-project in `.cloche/config.toml` (or similar):

```toml
[orchestration]
enabled = true
tracker = "beads"
concurrency = 1
workflow = "develop"
```

### Concurrency

Per-project limits. Project A with limit 2 and project B with limit 1 can have up to
3 runs in-flight simultaneously.

### Merge Queue

When a run completes successfully (`done` result), the orchestrator enqueues its
branch (`cloche/<runID>`) into a per-project merge queue.

**Serialization:** Per-project. Each project processes one merge at a time. Different
projects can merge in parallel. Queue is FIFO.

**Merge execution:** The orchestrator picks the next branch from the queue and hands
it to an LLM agent running on the host (not in a container). The agent operates in
a git worktree and:

1. Pulls latest main into the worktree.
2. Merges the feature branch.
3. Resolves any conflicts.
4. Runs validation if configured (tests, build).
5. If clean: merges to main, cleans up the worktree, deletes the branch.
6. If unresolvable: reports failure.

**On the host via worktree:** Merges need direct access to the real git repo. A git
worktree is created for the merge, then removed after completion. Lighter weight
than spinning up a container.

**Failure handling:** If the merge agent can't resolve conflicts, the merge is marked
as failed. The branch is preserved. The user is notified via the dashboard/indicator
lights. No automatic retry — human intervenes.

**Post-merge:** After a successful merge, the branch is deleted and the orchestrator
triggers a check for more work (same event-driven model as run completion).

---

## 2. Indicator Lights

**Goal:** Per-project health indicators based on recent run history.

### Health Calculator

Domain-level function: given the last N runs for a project, return health status:
- **Green** — all runs passed
- **Yellow** — some runs failed
- **Red** — all runs failed
- **Grey** — no runs yet
- **Blue** — all runs currently in-progress (no completed runs in window)

Window size N is configurable per-project, default 5.

### API

Extend `GET /api/projects` to include a `health` field per project:

```json
{
  "name": "my-project",
  "health": {
    "status": "yellow",
    "passed": 3,
    "failed": 2,
    "total": 5
  }
}
```

### CLI

`cloche health` — table of all projects with health status, passed/failed counts.

### Dashboard

Colored dot next to each project name on the landing page.

---

## 3. Web Dashboard Enhancements

**Goal:** Project-centric landing page with health indicators, run history, and quick
actions. Enabled by default.

### Landing Page

Replace the current runs-list with a project overview. Each project card shows:
- Indicator light (health status color)
- Success rate (e.g. "8/10 recent runs passed")
- Mini run history — last ~10 runs as colored dots (green/red)
- Active run count
- Quick actions: "Trigger orchestrator" button, "View runs" link

### Default Activation

Dashboard starts automatically with `cloched` on default port `:8080`.
Overridable via `CLOCHE_HTTP` env var. Disable with `CLOCHE_HTTP=off`.

### Existing Pages

Runs list and run detail pages preserved, accessible per-project from the landing page.

---

## 4. Log Contents

**Goal:** Capture all output (LLM conversations, script output, status messages) in a
unified log stream. Real-time streaming for active runs, archived files for completed runs.

### Unified Log Stream

The agent captures everything chronologically to `.cloche/output/full.log` with
structured entries:

```
[2026-03-03T10:15:00Z] [status] step_started: build
[2026-03-03T10:15:01Z] [script] + npm run build
[2026-03-03T10:15:02Z] [script] Build successful
[2026-03-03T10:15:03Z] [status] step_completed: build -> done
[2026-03-03T10:15:04Z] [status] step_started: implement
[2026-03-03T10:15:05Z] [llm] Claude: I'll start by reading the relevant files...
[2026-03-03T10:15:06Z] [llm] Claude: [tool_use: Read file src/main.go]
```

LLM output and script output are included inline in the unified stream. This is the
primary view for understanding what's happening.

### Per-Step Files

Additionally, per-step files are written for convenience:
- `.cloche/output/llm-<step>.log` — LLM conversation for agent steps
- `.cloche/output/<step>.log` — script output (existing behavior)

### Real-Time Streaming

For active runs, the daemon pipes container stdout to web clients via Server-Sent Events
(SSE). The run detail page gets a live log viewer. LLM and script output are visible
in real-time, not just status changes.

CLI: `cloche logs <run-id> --follow` streams the unified log in real-time.

### Post-Run Archival

On completion, log files extracted to `.cloche/<run-id>/output/`. SQLite index stores:
run ID, step name, log file path, size, timestamps.

### CLI Enhancements

`cloche logs <run-id>` shows the full unified log.
`cloche logs <run-id> --step <name>` filters to a specific step.
`cloche logs <run-id> --follow` streams in real-time.

---

## 5. Container Deletion

**Goal:** Keep failed containers for debugging. Show retained containers in the dashboard.
CLI command to delete them.

### Retention Policy

- Failed runs: container always kept (automatic).
- Successful runs: container auto-deleted (existing behavior).
- `--keep-container` flag preserved: keeps container regardless of outcome.

### Container Tracking

When a container is kept, record in SQLite: Docker container ID, run ID, project,
creation time, status. Updated when container is deleted.

### CLI Command

`cloche delete <container-or-run-id>` — accepts Docker container name/ID or run ID.
Calls daemon via gRPC to remove the container and update the store.
Single container at a time.

### gRPC

New RPC: `DeleteContainer(container_id)` — daemon calls `docker rm` and updates store.

### Dashboard

Run detail page shows whether container is still available. If so, shows "Delete
container" button. Project overview shows count of retained containers.

---

## Feature Dependencies

The features have some natural ordering based on dependencies:

1. **Container Deletion** — standalone, no dependencies on other features.
2. **Indicator Lights** — standalone domain logic, needed by Dashboard.
3. **Log Contents** — standalone, changes agent + daemon. Needed by Dashboard for log viewer.
4. **Web Dashboard** — depends on Indicator Lights and Log Contents for full functionality.
5. **Orchestrator** — depends on a working task tracker. Largest feature. Can be developed in parallel with 1-3. Includes the merge queue subsystem.
