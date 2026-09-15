# Web Dashboard

The web dashboard is a browser UI for monitoring and managing Cloche runs — a single-page
console shell with live task tracking, without needing the CLI.

## Enabling the Dashboard

`cloche init` creates `~/.config/cloche/config` with the dashboard enabled on
`localhost:8080` by default. Start the daemon and open `http://localhost:8080`.

To enable it manually, set `http` in `~/.config/cloche/config`:

```toml
[daemon]
http = "localhost:8080"
```

Or pass it as an environment variable:

```
CLOCHE_HTTP=localhost:8080 cloched
```

The dashboard is not started if `http` is unset (and `CLOCHE_HTTP` is not set).
Choose any available port. For a specific interface:

```toml
[daemon]
http = "127.0.0.1:8080"
```

The CLI command `cloche health` also requires `CLOCHE_HTTP` to be set, since it talks
to the daemon's HTTP API. `cloche tasks` falls back to `localhost:8080` when `CLOCHE_HTTP`
is unset.

If the configured port is unavailable when the daemon starts (e.g. still held by a
previous daemon that hasn't fully exited), the daemon does not give up on the dashboard —
it retries the bind with exponential backoff (1s up to 30s) until it succeeds or the
daemon shuts down. While down, the dashboard's status is visible via `cloche status` and
`cloche health` instead of just failing to connect.

---

## The Console

The dashboard is a single page — a "console shell" — built from a tab bar, a task stack,
and a centre pane, routed by project and task rather than by a fixed set of pages.

### Tab bar

Across the top: one tab per registered project (a health dot, a running-count badge, and
a `!` attention flag when something needs you), with idle projects (no active runs, nothing
needing attention) folded into a **More** menu to keep the bar short. Clicking a tab
switches projects without a page navigation.

On the right, daemon instruments for the active project:

- **Start loop / Stop loop** — toggles the orchestration loop (`POST /trigger` to start,
  `POST /loop/stop` to stop).
- **Slots** — busy/max concurrency slots (`GET /loop/occupancy`).
- **Queue** — how many tasks are waiting for a free slot.
- **Burn** — combined token burn rate across agents over the last hour (`GET /usage`).
- **Version** — the daemon's version.

### Task stack

The left rail groups the active project's tasks — **Needs you**, **Running**, **Queued**,
**Done today** (with a **Load earlier** button paging further into history) — from
`GET /api/projects/{slug}/tasks/stack`. It polls every few seconds using conditional GET
(`ETag`/`If-None-Match`), so a poll with nothing new costs a 304 rather than a re-render;
when something *has* changed, only the affected rows update in place.

Click a row (or select it with `j`/`k` and press Enter) to open it in the centre pane.

### Centre pane

Shows the selected task's full detail:

- **Header** — task ID, a state pill (running / needs you / queued / succeeded / failed /
  parked — parked also shows how long the run has been parked, e.g. "parked · 12m"), the
  title, and state-dependent actions: running → Console (raw container output), Workflow
  (step/wire summary), Cancel; done → Open branch, Diff, Delete container; queued → Cancel;
  parked → Cancel. Needs-you tasks show no actions yet.
- **Facts row** — attempt tabs (number, run ID, outcome, duration; only shown when a task
  has more than one attempt; `[` and `]` switch between them), plus the selected attempt's
  top-level run ID, child run IDs, container ID and state, token usage per agent, the
  prompt file and git revision used, and (on a retried attempt) which step the previous
  attempt failed at. Backed by `GET /api/projects/{slug}/tasks/{taskId}/attempts` for the
  attempt list and `GET /api/runs/{id}` for the selected attempt's detail.
- **Step strip** — one horizontal segment per step of the top-level run, with a spawned
  child run's steps inlined in a row beneath the workflow step that spawned them (the
  flattened-run tree already used by `GET /api/runs/{id}`). Each segment shows a result
  dot and duration; poll steps also show their last poll time and count. The live (or
  selected) segment is highlighted. Clicking a segment scopes the log below to that step's
  output (`GET /api/runs/{id}/steps/{step}/output`); clicking it again clears the scope.
- **Log** — full pane width. Unscoped, it's an SSE stream (`GET /api/attempts/{id}/stream`)
  with a follow toggle, a replay of the last ~1000 lines, and "load earlier" paging
  (`GET /api/attempts/{id}/logs`). A type filter (all/llm/script/status), a wrap toggle,
  and `g`/`G` (scroll to top/bottom) are always available. Every line keeps its timestamp,
  type, and originating step. An SSE error shows "Disconnected" — only an explicit `done`
  event marks the stream "Complete". When the run is parked (see below), the log pane is
  replaced by the help-thread panel instead.

#### Parked pane

When the selected task's run is parked awaiting a help-thread reply, the step strip freezes
on the step that parked (a distinct dot color) and the log pane is replaced by the thread
panel: the agent's question and any prior exchanges (`GET /api/runs/{id}/thread`, resolved
from the run's parked thread), and a reply box. Submitting the reply box posts to
`POST /api/runs/{id}/thread/reply`, which the daemon serves through the same `ReplyThread`
RPC handler `cloche threads reply` uses — including resuming the run if the reply is what
it was waiting on. The centre pane keeps polling while parked, so once the run resumes the
step strip, facts, and log pane pick back up automatically.

### Routing

URLs follow `/{project-slug}` and `/{project-slug}/{task-id}`. Selecting a project or task
updates the URL via `history.pushState` without a page reload; browser back/forward works
as expected. Attempt and step-scope selection are client-side state, not reflected in the
URL yet.

The pre-console URLs (`/`, `/projects/{name}[/runs]`, `/runs[/{id}]`, `/tasks/{id}`,
`/failed-tasks`) redirect into the new scheme for one release before being removed.

### Keyboard

| Key | Action |
|-----|--------|
| `j` / `k` | Move the stack selection down / up |
| `Tab` / `Shift+Tab` | Switch to the next / previous project |
| `Enter` | Open the selected task in the centre pane |
| `Esc` | Close an open drawer/view, else return to the stack (clears the centre pane) |
| `a` | Open the activity stream |
| `[` / `]` | Switch to the previous / next attempt |
| `g` / `G` | Scroll the log to the top / bottom |
| `w` | Open the Workflows view |
| `i` | Open the Intent view |
| `c` | Open the Containers view |
| `?` | Toggle the keyboard shortcuts overlay |

### Foot bar

Shows the keybindings on the left and a one-line activity ticker on the right — the most
recent `activity_log` event for the active project, polled from `GET /api/activity`
every 5s. Repeated events with the same signature (project, kind, workflow, step,
outcome) on the same day collapse into one line with an ordinal count, e.g.
`intent-scan failed · 8th today`, reusing the day-scoped grouping approach from the
built-in-failure-summary grouping (see `buildBuiltinFailureSummaries` in
`internal/adapters/web/handler.go`).

Clicking the ticker (or pressing `a`) expands it into a scrollable activity stream
overlay, with filters for **This project** / **All projects** and **Failures only**. The
stream is always a bounded tail (never the full `activity_log` table): the first page
covers today plus a fixed page size, and a **Load earlier** button pages further into
history via an opaque cursor (an `activity_log` row ID), the same pattern the task
stack's "Done today" group uses for its own cursor.

### Secondary views: Workflows, Intent, Containers

Three header buttons (and matching shortcuts `w` / `i` / `c`) open project-scoped views as
an overlay on top of the console shell — they don't navigate away from the current
project/task URL. `Esc` closes the topmost open drawer first, then the view itself, before
falling back to the stack-deselect behavior described above.

- **Workflows** — the read-only DAG of steps/wires for the active project's container and
  host workflows (`GET /api/projects/{slug}/workflows`), with location/workflow tabs when
  more than one applies. Built-in workflows are marked with a `built-in` badge. Clicking a
  step node opens a drawer with its type, results, and whichever of the DSL's real config
  keys it uses (`prompt`, `run`, `poll`, `interval`, `agent`, `agent_command`, `agent_args`,
  `intent_tracking`, `workflow_name`, `max_attempts`), plus the referenced prompt/script
  content (`GET /api/projects/{slug}/workflows/{workflow}/steps/{step}/content`).
- **Intent** — the requirements table, its edit drawer, and the domain editor, driven by
  the same `GET/PATCH /api/projects/{slug}/intent/requirements` and
  `GET/PUT /api/projects/{slug}/intent/domains` endpoints as before. **Scan now**
  (`POST /api/projects/{slug}/intent/scan`) shows the dispatched run's id and live state
  next to the button, with a link to jump straight to that run's task in the stack, instead
  of silently reloading the requirements table.
- **Containers** — retained containers for the project, grouped by task, each row showing
  size and age (`GET /api/projects/{slug}/containers`). Delete a single container
  (`DELETE /api/runs/{id}/container`) or clean up every retained container in the project
  (`DELETE /api/projects/{slug}/containers`) — there is no cross-project clean-up button;
  the daemon-wide `DELETE /api/containers` endpoint still exists but nothing in the
  dashboard links to it, since a single click there could silently remove another
  project's kept containers.

---

## JSON API

The dashboard's JSON endpoints remain stable and are also used by the CLI
(`cloche health`, `cloche tasks`, etc.):

- `GET /api/projects` — per-project health, active-run count, and attention count.
- `GET /api/projects/{name}/tasks` — the orchestration loop's live task snapshot.
- `GET /api/projects/{name}/tasks/stack` — the grouped, bounded task stack (see above).
- `GET /api/projects/{name}/tasks/{taskId}/attempts` — a task's attempts (oldest first),
  each with its run ID, outcome, duration, and retry reason — backs the centre pane's
  attempt tabs.
- `GET /api/projects/{name}/loop/status`, `POST /loop/stop`, `POST /trigger` — loop control.
- `GET /api/projects/{name}/loop/occupancy`, `GET /api/projects/occupancy` — concurrency
  slots and queue depth, per-project and all-projects.
- `GET /api/projects/{name}/usage` — token burn rate and 24h totals.
- `GET /api/runs`, `GET /api/runs/{id}`, `GET /api/runs/{id}/stream` — run listing, detail
  (steps, child runs, container state, per-agent token totals, prompt file/git revision),
  and live SSE log streaming.
- `GET /api/runs/{id}/steps/{step}/output` — a single step's raw output, for the step strip.
- `GET /api/runs/{id}/thread`, `POST /api/runs/{id}/thread/reply` — the help thread a parked
  run is awaiting a reply on, and posting a reply (same path as `cloche threads reply`,
  including resuming the run). 404 when the run isn't parked.
- `GET /api/runs/{id}/branch`, `GET /api/runs/{id}/diff` — the run's extracted result
  branch(es) and their diff against the run's base revision.
- `GET /api/runs/{id}/console` — the raw (unparsed) container log, for the header's Console
  action.
- `GET /api/attempts/{id}/stream`, `GET /api/attempts/{id}/logs` — SSE streaming and
  paginated log lines across an attempt's host run and any spawned child runs.
- `GET /api/failed-tasks` — failed-but-still-open tasks and built-in workflow failures.
- `GET /api/activity` — the activity ticker/stream (see Foot bar above), filtered by
  `?project=<slug>` (all projects when omitted) and `?failures_only=1`, paged via
  `?before=<cursor>&limit=<n>`.
- `GET /api/projects/{name}/intent/...` — intent requirements, domains, and scan control.
- `GET /api/projects/{name}/workflows`, `GET .../workflows/{workflow}/steps/{step}/content` —
  workflow structure and step content for the Workflows view's DAG and drawer.
- `GET /api/projects/{name}/containers`, `DELETE .../containers`, `DELETE /api/runs/{id}/container` —
  per-task retained-container listing (with size/age), project-scoped clean-up, and
  per-container delete for the Containers view.

These are unchanged by the console-shell rework; only the HTML pages that used to render
around them were replaced.
