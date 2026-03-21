# Version 2.0.0: Task-Oriented APIs Design

**Date:** 2026-03-18
**Status:** Partially Implemented (gRPC API complete: RunWorkflow returns task_id + attempt_id, ListTasks, GetTask, GetAttempt RPCs added, StreamLogs and GetStatus accept hierarchical IDs. Web UI: task list landing page, task drill-down at `/tasks/{id}`, SSE at `/api/attempts/{id}/stream`, v2 log paths, attempt aggregate status, task status reflects latest attempt, step-level log routing via step_name inference). CLI and migration work remain.

## Problem

Cloche's APIs (CLI, Web UI, gRPC) are oriented around workflow step runs. Users
interact with flat lists of runs identified by opaque IDs like
`develop-proud-nest-06cd`. In practice, the natural unit of work is the Task — a
ticket from the task tracker that may require multiple attempts, each involving
host and container workflows with multiple steps. The current model forces users
to mentally reconstruct the task/attempt hierarchy from flat run lists.

## Solution

Promote Task and Attempt to first-class domain concepts. Every workflow
execution belongs to a Task and an Attempt. The CLI, Web UI, and gRPC APIs
default to task-oriented views. Run IDs change to a colon-delimited format that
encodes the hierarchy: `attempt:workflow:step`. Existing data is automatically
migrated on first v2 startup.

---

## Design Details

### Domain Model

Three new/refined concepts:

**Task** — The top-level work unit. Has an ID from the ticket tracker (e.g.
`cloche-123`). For manual `cloche run` invocations without a task ID, a
"User-Initiated" task is auto-generated with a unique ID.

**Attempt** — One try at completing a task. Has a short generated ID (e.g.
`a12z`), start time, end time, and a tracked list of all workflow step
executions triggered. A new attempt is created each time the orchestration loop
picks up a task ID, or when a user manually runs a workflow against an existing
task.

**Step Execution** — A single workflow step within an attempt. Identified by the
colon-delimited ID `attempt:workflow:step` (e.g. `a12z:develop:implement`). A
full workflow execution is addressed as `attempt:workflow` (e.g.
`a12z:develop`).

```
Task (cloche-123)
├── Attempt (a12z)
│   ├── a12z:main:prepare-prompt
│   ├── a12z:develop:implement
│   ├── a12z:develop:test
│   └── a12z:main:finalize
└── Attempt (b34x)
    ├── b34x:main:prepare-prompt
    ├── b34x:develop:implement
    ├── b34x:develop:test
    └── b34x:main:finalize
```

#### Domain types

New types in `internal/domain/`:

```go
type Task struct {
    ID         string
    Title      string
    Status     TaskStatus // derived from latest attempt
    Source     TaskSource // "external" | "user-initiated"
    ProjectDir string
    CreatedAt  time.Time
    Attempts   []*Attempt
}

type Attempt struct {
    ID        string
    TaskID    string
    StartedAt time.Time
    EndedAt   time.Time
    Result    AttemptResult // "succeeded", "failed", "cancelled", "running"
}
```

The existing `Run` struct is replaced or significantly narrowed. Step executions
link to an attempt via `AttemptID` and are identified by the
`attempt:workflow:step` triple.

Task status is derived from the **latest attempt only** — no more aggregating
across all runs for a task.

### Database Schema

New tables:

```sql
CREATE TABLE tasks (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'external',  -- 'external' | 'user-initiated'
    project_dir TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE attempts (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL REFERENCES tasks(id),
    started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    ended_at DATETIME,
    result TEXT NOT NULL DEFAULT 'running',
    project_dir TEXT NOT NULL DEFAULT ''
);
```

The existing `runs` table is refactored to track step executions, with an
`attempt_id` column replacing the current standalone identity. Existing columns
that belong to the task or attempt level (`task_id`, `task_title`, etc.) move to
their respective tables.

A join table links log files to attempts:

```sql
CREATE TABLE IF NOT EXISTS attempt_logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    attempt_id TEXT NOT NULL,
    task_id TEXT NOT NULL,
    file_type TEXT NOT NULL,
    file_path TEXT NOT NULL,
    file_size INTEGER,
    created_at TEXT NOT NULL,
    FOREIGN KEY (attempt_id) REFERENCES attempts(id)
);
```

### ID Format

**Attempt IDs** are short, URL-safe, human-typeable strings (4 alphanumeric
characters, e.g. `a12z`). Generated randomly with collision retry.

**Step execution IDs** are colon-delimited: `attempt:workflow:step`.
- `a12z` — the attempt
- `a12z:develop` — a workflow execution within the attempt
- `a12z:develop:implement` — a specific step

This format is used in CLI output and API responses. The colon delimiter is
chosen because it is shell-safe (no quoting needed) and visually distinct.
**Colons are never used in filesystem paths** — log files use the
task/attempt directory hierarchy with dash-delimited filenames.

### Log File Layout

Logs move from the current per-run layout:

```
.cloche/<run-id>/output/<step>.log
```

To a task/attempt hierarchy:

```
.cloche/logs/<task-id>/<attempt-id>/<step>.log
```

Example: `.cloche/logs/cloche-123/a12z/prepare-prompt.log`

Each host workflow run (e.g. `main`) writes its step logs into the shared
attempt directory. Workflow-dispatch steps (e.g. `main:develop`) capture the
child run ID as their step output, so `develop.log` contains the dispatched run
ID rather than container-side output. Container-side step logs still live under
`.cloche/<run-id>/output/` until full v2 migration is complete.

The `full.log` unified log file is at
`.cloche/logs/<task-id>/<attempt-id>/full.log`.

### CLI Changes

#### `cloche list`

Default view becomes task-oriented:

```
TASK ID       STATUS     TITLE                           ATTEMPTS  LATEST
cloche-123    running    Fix the card renderer            2         b34x
cloche-456    succeeded  Add pagination to search         1         c78y
user-a1b2     failed     (user-initiated)                 1         d90z
```

Flags:
- `--all` — all projects
- `--project <dir>` — specific project
- `--state <s>` — filter by task status (derived from latest attempt)
- `--limit <n>` — limit results

#### `cloche status <id>`

Accepts either a task ID or an attempt-prefixed ID:

- `cloche status cloche-123` — shows task with all attempts, steps in latest
  attempt
- `cloche status a12z` — shows a specific attempt with its steps
- `cloche status a12z:develop` — shows steps within a specific workflow
  execution
- `cloche status a12z:develop:implement` — shows detail for one step

#### `cloche logs <id>`

- `cloche logs a12z:develop:implement` — logs for a specific step
- `cloche logs a12z:develop` — interleaved logs for all steps in a workflow
- `cloche logs a12z` — interleaved logs for the full attempt

The existing `-f`, `-l`, `-s`, `--type` flags continue to work.

#### `cloche run`

- `cloche run --workflow develop --prompt "..."` — creates a User-Initiated task
  and attempt
- `cloche run --workflow develop --issue cloche-123 --prompt "..."` — creates a
  new attempt under existing task

### Web UI Changes

**Implemented.** The landing page (`/`) shows project badges (health dot,
pass/total stats, active count, recent run history). The Tasks section has been
removed from the landing page. A task drill-down page at `/tasks/{id}` shows all
attempts for a task. The runs list (`/projects/{name}/runs`) groups runs by
task with attempt headers.

**Attempt status** is computed by `domain.AttemptAggregateStatus`: if any
workflow run within an attempt is still active (running or pending) that state
takes precedence; otherwise the worst terminal state wins — failed > cancelled >
succeeded. This means a single failed child run marks the entire attempt as
failed. When multiple runs share the same workflow name (e.g. a finalize workflow
that was re-run after a failure), only the most recently started run is
considered — earlier runs of the same workflow are superseded and do not affect
the aggregate result.

**Task status** always reflects the status of the task's latest (most recent)
attempt, not an aggregate across all historical attempts.

```
▼ cloche-123 — Fix the card renderer          [running]
    ▶ Attempt b34x (running)   2026-03-18 07:15
    ▶ Attempt a12z (failed)    2026-03-18 06:30
▼ cloche-456 — Add pagination to search       [succeeded]
    ▶ Attempt c78y (succeeded) 2026-03-18 05:00
```

The step detail view (accordion) shows per-step log output. Each `LogLine`
emitted by the SSE stream and paginated log API carries a `step_name` field
inferred by `readVisibleLogLines`: it tracks `step_started: <name>` and
`step_completed: <name> -> <result>` status entries in the unified `full.log`
and tags every line between them with the corresponding step name. The frontend
routes each line to the matching step panel. Lines outside any step boundary
have an empty `step_name`.

Both SSE endpoints are active: `/api/runs/{id}/stream` (legacy) and the new
`/api/attempts/{id}/stream` which streams all step output for an attempt.
Paginated log access is available at `/api/attempts/{id}/logs`.

### gRPC API Changes

This is a v2.0.0 breaking change. Key RPC changes:

- `RunWorkflow` already accepts an attempt ID propagated from the host executor
  via the `x-cloche-attempt-id` gRPC metadata header, so child container runs
  are linked to the parent host run's attempt.
- `RunWorkflow` response includes `task_id` and `attempt_id` (instead of just
  `run_id`) — *not yet implemented; currently returns only `run_id`*
- `ListRuns` becomes `ListTasks` — returns tasks with latest attempt info
- New `GetTask(task_id)` RPC — returns task with all attempts
- New `GetAttempt(attempt_id)` RPC — returns attempt with step executions
- `StreamLogs` accepts the colon-delimited ID format at any level
- `GetStatus` accepts task ID, attempt ID, or full step ID

### Shell Auto-Completion

`cloche init` sets up shell completion for zsh and bash. The completion scripts
are written to a standard location and the user's shell rc file is updated.

#### Installation

During `cloche init`:
1. Generate completion scripts using Go's `cobra` or custom completion logic
2. Write scripts to `~/.cloche/completions/` (user-local, no sudo needed)
3. Check if `.zshrc` / `.bashrc` already sources the completion script
4. If not, offer to append the source line
5. Skip on Windows

#### What completes

- **Subcommands**: `cloche <TAB>` → `run`, `list`, `status`, `logs`, `stop`,
  `init`, `loop`, ...
- **Task IDs**: `cloche status <TAB>` → queries daemon for recent task IDs
- **Attempt IDs**: `cloche status a<TAB>` → queries daemon for matching attempts
- **Colon-delimited drill-down**: `cloche logs a12z:<TAB>` → offers workflow
  names; `cloche logs a12z:develop:<TAB>` → offers step names
- **Workflow names**: `cloche run --workflow <TAB>` → reads `.cloche/*.cloche`
  files
- **Flags**: `cloche list --<TAB>` → `--all`, `--state`, `--limit`, etc.

Completion queries the daemon via a lightweight gRPC call (new `Complete` RPC
that returns matching IDs given a prefix and context).

### Automatic Migration (v1 → v2)

On first v2 daemon startup, if the database contains v1-format runs:

1. **Create tasks** — Group existing runs by `task_id`. Runs with empty
   `task_id` get a generated User-Initiated task.
2. **Create attempts** — Each run becomes an attempt. Multiple runs with the
   same `task_id` become separate attempts ordered by `started_at`.
3. **Migrate step data** — Step execution records link to their new attempt.
4. **Move log files** — For each migrated run, move files from
   `.cloche/<run-id>/output/` to `.cloche/logs/<task-id>/<attempt-id>/`. The old
   directory is removed after successful copy.
5. **Rename containers** — Docker containers named with old run IDs continue to
   work (they are addressed by container ID internally). No container rename
   needed.

The migration is idempotent — a version marker in the database prevents
re-running.

### Error Handling

- If migration fails partway, the version marker is not set, so it retries on
  next startup. Individual run migrations that fail are logged and skipped.
- If the daemon cannot reach the task tracker, User-Initiated task IDs are
  generated locally (no external dependency for ID generation).
- Shell completion gracefully degrades — if the daemon is not running,
  subcommands and flags still complete (static); only dynamic completions (task
  IDs, attempt IDs) are skipped.

## Alternatives Considered

**Keep runs as the primary concept, just improve grouping** — Rejected because
the mismatch between what users care about (tasks) and what the system tracks
(runs) creates friction at every layer. Better to fix the model than paper over
it with UI grouping.

**Use the existing run ID format with a task prefix** — e.g.
`cloche-123/develop-proud-nest-06cd`. Rejected because it doesn't solve the
attempt tracking problem and the IDs become unwieldy.

**Store completion scripts in `/usr/local`** — Rejected in favor of
`~/.cloche/completions/` to avoid requiring sudo and to keep everything
user-local.

## Resolved Questions

- **`cloche list` verbosity**: Per-attempt detail lives in `cloche status
  <task-id>`. `cloche list` stays concise — one row per task.
- **Attempt ID generation**: Random 4-character alphanumeric (lowercase +
  digits). Uniqueness is scoped to the parent task, not global. On generation,
  retry if the ID collides with an existing attempt for the same task.
