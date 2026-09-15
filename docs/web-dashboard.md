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

Shows the selected task's header and a facts row (status, timing, current step, or outcome,
depending on which group it came from). This is a placeholder host — the full detail pane
(logs, steps, DAG, etc.) is a separate ticket.

### Routing

URLs follow `/{project-slug}` and `/{project-slug}/{task-id}`; `?attempt=` and `?step=`
are reserved for a future detail pane. Selecting a project or task updates the URL via
`history.pushState` without a page reload; browser back/forward works as expected.

The pre-console URLs (`/`, `/projects/{name}[/runs]`, `/runs[/{id}]`, `/tasks/{id}`,
`/failed-tasks`) redirect into the new scheme for one release before being removed.

### Keyboard

| Key | Action |
|-----|--------|
| `j` / `k` | Move the stack selection down / up |
| `Tab` / `Shift+Tab` | Switch to the next / previous project |
| `Enter` | Open the selected task in the centre pane |
| `Esc` | Return to the stack (clears the centre pane) |
| `a` | Open the activity stream |
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

---

## JSON API

The dashboard's JSON endpoints remain stable and are also used by the CLI
(`cloche health`, `cloche tasks`, etc.):

- `GET /api/projects` — per-project health, active-run count, and attention count.
- `GET /api/projects/{name}/tasks` — the orchestration loop's live task snapshot.
- `GET /api/projects/{name}/tasks/stack` — the grouped, bounded task stack (see above).
- `GET /api/projects/{name}/loop/status`, `POST /loop/stop`, `POST /trigger` — loop control.
- `GET /api/projects/{name}/loop/occupancy`, `GET /api/projects/occupancy` — concurrency
  slots and queue depth, per-project and all-projects.
- `GET /api/projects/{name}/usage` — token burn rate and 24h totals.
- `GET /api/runs`, `GET /api/runs/{id}`, `GET /api/runs/{id}/stream` — run listing, detail,
  and live SSE log streaming.
- `GET /api/failed-tasks` — failed-but-still-open tasks and built-in workflow failures.
- `GET /api/activity` — the activity ticker/stream (see Foot bar above), filtered by
  `?project=<slug>` (all projects when omitted) and `?failures_only=1`, paged via
  `?before=<cursor>&limit=<n>`.
- `GET /api/projects/{name}/intent/...` — intent requirements, domains, and scan control.

These are unchanged by the console-shell rework; only the HTML pages that used to render
around them were replaced.
