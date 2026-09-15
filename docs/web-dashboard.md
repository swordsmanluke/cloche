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
an attention flag when something needs you). The fold rule always keeps the active
project and anything with live activity (a running loop, active runs, or attention items)
visible, then fills a fixed budget of remaining slots with the most recently active
projects (by latest run start); only genuinely stale, inactive projects beyond that budget
fold into a **More** menu. This keeps a stopped loop with no current activity from folding
every other project down to a single visible tab. Clicking a tab switches projects without
a page navigation. A staleness hint appears next to the tab bar when the cached attention
data behind `attention_count` hasn't refreshed recently.

On the right, daemon instruments for the active project:

- **Loop** — toggles the orchestration loop, showing "running"/"stopped" (`POST /trigger`
  to start, `POST /loop/stop` to stop).
- **Slots** — busy/max concurrency slots plus how many tasks are waiting for a free slot
  (`GET /loop/occupancy`).
- **Burn** — combined token burn rate across agents over the last hour (`GET /usage`).
- **Version** — the daemon's version.
- **Ledger** — opens the project ledger overlay (see below).

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
  parked → Cancel. A needs-you task's actions come from the attention item's own `actions`
  list (see below) rather than a fixed set per state.
- **Needs-you why-line and compare view** — when the selected task's attention kind is
  `stale-claim` or `repeat-failure`, a one-sentence why-line (the attention item's
  `reason`) appears under the header, and the log area defaults to a compare view: one
  column per failed attempt (the last three; older ones are not yet individually
  selectable), each showing the failing step's log trimmed to start at its first
  failure-looking line, fetched from `GET /api/runs/{id}/steps/{step}/output` using the
  `failed_step` named on each attempt (`GET /api/projects/{slug}/tasks/{taskId}/attempts`).
  Press `c` to toggle back to the ordinary single-attempt log, and again to return to the
  compare view. Available actions, driven by the item's `actions` list: **Release claim**
  (`POST .../tasks/{taskId}/release`), **Close in tracker** (`POST .../tasks/{taskId}/close`
  — runs the project's `close-task`/`cancel-task` host workflow contract; disabled with a
  hint when the project defines neither), and **Run once…** (`POST
  .../tasks/{taskId}/run-once` with a chosen workflow name and optional prompt — dispatches
  a single attempt outside the orchestration loop). A `builtin-failures` item instead offers
  **Mute** (`POST /api/projects/{slug}/attention/mute`), which permanently suppresses that
  workflow's repeated-failure item. All four actions refresh the task stack in place
  (`GET .../tasks/stack`) rather than reloading the page.
- **Facts row** — a single wrapping strip of label/value pairs. When a task has more than
  one attempt, its leading entry is a group of attempt chips (attempt number + short run
  ID; outcome and duration are in the chip's tooltip; the current attempt and any failed
  attempts are colored distinctly; `[` and `]` switch between them). The rest of the strip
  is the selected attempt's top-level run ID, child run IDs, container ID and state, token
  usage per agent, the prompt file and git revision used, and (on a retried attempt) which
  step the previous attempt failed at. Backed by `GET /api/projects/{slug}/tasks/{taskId}/attempts`
  for the attempt list and `GET /api/runs/{id}` for the selected attempt's detail.
- **Step strip** — one horizontal segment per step of the top-level run, with a spawned
  child run's steps inlined immediately after the workflow step that spawned them (the
  flattened-run tree already used by `GET /api/runs/{id}`) — the parent segment gets a `↳`
  marker and its children render shaded and indented in the same strip. Each segment shows
  a result dot and duration; poll steps also show their last poll time and count. The live
  (or selected) segment is highlighted. Clicking a segment scopes the log below to that
  step's output (`GET /api/runs/{id}/steps/{step}/output`); clicking it again clears the
  scope.
- **Log** — full pane width, topped by a status band (not toolbar-style controls): a scope
  chip (`log`, or `log · <step name> ✕` when scoped — clicking it clears the scope), a type
  filter as chips (all/llm/script/status), a click-to-toggle live/follow indicator, and a
  right-aligned line count, wrap toggle, and `g`/`G` (scroll to top/bottom) hint. Unscoped,
  the pane is an SSE stream (`GET /api/attempts/{id}/stream`) with a replay of the last
  ~1000 lines and "load earlier" paging (`GET /api/attempts/{id}/logs`). Every line keeps
  its timestamp, type, and originating step, and its content is colorized by a simple
  classifier (tool-call markers, pass/fail/warning keywords, and step-transition
  success/failure). The live indicator reads "● live" (plus "· following" while follow is
  on — click it to toggle), "complete" once an explicit `done` event closes the stream, or
  "disconnected" on an SSE error. When the run is parked (see below), the log pane is
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

The pre-console URLs (`/projects/{name}[/runs]`, `/runs[/{id}]`, `/tasks/{id}`,
`/failed-tasks`) redirect into the new scheme for one release before being removed.
`GET /` no longer redirects — it renders the console shell directly at `/`, seeded with a
best-effort landing project (the project with the most recently started run, falling back
to alphabetical-by-slug). The client then prefers the last project the user actually
viewed, if any, persisted client-side in `localStorage`, upgrading to the server's pick
once the project list loads if that preference is empty or stale.

### Keyboard

| Key | Action |
|-----|--------|
| `j` / `k` | Move the stack selection down / up |
| `Tab` / `Shift+Tab` | Switch to the next / previous project |
| `Enter` | Open the selected task in the centre pane |
| `Esc` | Close an open drawer/view, else return to the stack (clears the centre pane) |
| `a` | Open the activity stream |
| `l` | Open the project ledger |
| `[` / `]` | Switch to the previous / next attempt |
| `g` / `G` | Scroll the log to the top / bottom |
| `f` | Toggle following the live log to its newest line |
| `r` | Release your claim on the open needs-you task (when available) |
| `x` | Close the open needs-you task in the tracker (when available) |
| `w` | Open the Workflows view |
| `i` | Open the Intent view |
| `c` | Open the Containers view, or toggle the needs-you compare view / single-attempt log when one is open |
| `?` | Toggle the keyboard shortcuts overlay |

### Foot bar

Shows a one-line activity ticker on the left and a short, contextual set of key hints on
the right — the ticker is the only real content, so it gets the width; the hints are
chrome that changes with the selected task's state rather than always listing every
shortcut (the full list stays behind `?`). A running task shows `j`/`k`, `[`/`]`, `tab`,
`f`, `a`; a needs-you task shows `j`/`k`, `[`/`]`, and whichever of `r`/`x` its attention
item actually offers; anything else falls back to a short baseline. `g`/`G` and the log
type filter live in the log bar instead, since they act on the log pane, not the task.

The ticker packs as many of the most recent `activity_log` events for the active project
as fit on the line (newest first, polled from `GET /api/activity` every 5s), each prefixed
with its `HH:MM:SS` time and separated by ` · `, with failed entries coloured red. Repeated
events with the same signature (project,
kind, workflow, step, outcome) on the same day collapse into one line with an ordinal
count, e.g. `intent-scan failed · 8th today`, reusing the day-scoped grouping approach
from the built-in-failure-summary grouping (see `buildBuiltinFailureSummaries` in
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

### Ledger

The **Ledger** instrument button (or pressing `l`) opens a per-project overlay
summarizing outcomes across every attempt, from `GET /api/projects/{slug}/ledger`:

- **Summary** — mean attempts to success, mean tokens per succeeded task, and the
  latest day's pass rate, plus a day-by-day pass-rate table.
- **Prompt revisions** — for each prompt file used by an agent step (`prompt =
  file("...")` in the workflow DSL), its outcome stats broken out by git revision
  (newest first, from `git log --follow`), and a before/after comparison for the most
  recent edit that has recorded attempts.
- **Requirements** — standing requirements (from `.cloche/intent/`) cross-referenced
  with the tasks whose steps ran with them injected, and the reverse index (task →
  requirement IDs).

Attribution of an attempt to a prompt revision is recorded at step-dispatch time
(`host.Executor.recordPromptRevisionKV` and its `grpc.DaemonExecutor` counterpart for
container steps), keyed by the resolved prompt file and the git commit that last
touched it as of dispatch. Attempts that predate this recording are backfilled
best-effort using the workflow's current prompt-file references and `git log` history
at the attempt's start time. Escape or `l` closes the overlay.

---

## JSON API

The dashboard's JSON endpoints remain stable and are also used by the CLI
(`cloche health`, `cloche tasks`, etc.):

- `GET /api/projects` — per-project health, active-run count, attention count
  (`attention_count`/`attention_computed_at`, read from the background attention cache —
  see `internal/attention.Cache` — rather than computed per request), and `loop_running`/
  `latest_run_at` (used by the tab bar's fold rule and the "/" landing-project fallback).
- `GET /api/projects/{name}/attention` — the full "Needs you" item list for one project
  plus `computed_at`, from the same cache; `computed_at` is empty for a project that
  hasn't been refreshed yet.
- `GET /api/projects/{name}/tasks` — the orchestration loop's live task snapshot.
- `GET /api/projects/{name}/tasks/stack` — the grouped, bounded task stack (see above).
- `GET /api/projects/{name}/tasks/{taskId}/attempts` — a task's attempts (oldest first),
  each with its run ID, outcome, duration, retry reason, and the step it itself failed at
  (`failed_step`) — backs the centre pane's attempt tabs and the needs-you compare view.
- `POST /api/projects/{name}/tasks/{taskId}/release` — releases a stale claim back to open.
- `POST /api/projects/{name}/tasks/{taskId}/close` — runs the project's `close-task`/
  `cancel-task` host workflow contract for the task; `501` with a `hint` when neither is
  defined.
- `POST /api/projects/{name}/tasks/{taskId}/run-once` — dispatches a single attempt of a
  named workflow (and optional prompt) for the task, outside the orchestration loop.
- `POST /api/projects/{name}/attention/mute` — mutes a "Needs you" item by its `key`
  (see `GET .../tasks/stack`'s `needs_you[].key`); currently only meaningful for
  `builtin-failures` items.
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
- `GET /api/projects/{name}/ledger` — pass rate over time, mean attempts/tokens to
  success, per-prompt-file revision outcomes, and requirement-injection
  cross-references (see Ledger above).

These are unchanged by the console-shell rework; only the HTML pages that used to render
around them were replaced.
