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

A synthetic **System** tab (slug `system`) groups tasks and runs with no owning project —
e.g. a `cloche run` invoked outside a registered project. It only appears once such a row
actually exists, and has no orchestration loop of its own (the Loop instrument is disabled
there).

### Repo sub-tabs

A project declaring more than one `[[repositories]]` entry in `.cloche/config.toml` gets a
second row directly beneath the project tab row: a small "repo" label, an **all repos**
pseudo-tab (the default — today's merged view), then one sub-tab per configured
repository, each carrying the same running-count/attention badges as project tabs.
Selecting a repo sub-tab scopes the task stack to that repo only — the other repos' rows
are removed from the stack entirely, not merely labelled. Legacy projects (no
`[[repositories]]`, or exactly one — the auto-seeded implicit default) render no sub-tab
row at all: the body grid goes straight from the project tab row to the stack, identical
to a project registered before this feature existed.

Which sub-tab a task appears under is decided by `internal/domain.ResolveRunRepositories`
(see the `[[repositories]]` reference in [USAGE.md](USAGE.md#repositories-1)), not just
the workflow's own single-repo `repos` declaration — a host workflow that declares no
`repos` at all (the documented default: "gets every configured repository") can still be
attributed to the repos it actually touches, via a step's `repository` config key or the
repos a container sub-workflow extracted results into, and a workflow declaring more than
one `repos` entry is attributed to all of them. A task can therefore appear under more
than one repo sub-tab at once; **all repos** always shows the union. A task matching none
of these rules is unattributed — it still appears under **all repos** only, and is folded
into that tab's counts as a separate "unattributed" figure rather than silently missing
from every named sub-tab.

On the right, daemon instruments for the active project:

- **Loop** — toggles the orchestration loop, showing "running"/"stopped" (`POST /trigger`
  to start, `POST /loop/stop` to stop); disabled on the System tab, which has no loop.
- **Slots** — busy/max concurrency slots plus how many tasks are waiting for a free slot
  (`GET /loop/occupancy`).
- **Burn** — combined token burn rate across agents over the last hour (`GET /usage`).

A **Tools ▾** button next to the instruments opens a menu with the Workflows,
Requirements, Containers, and Ledger entries (see below); the daemon's version is shown
in the foot bar instead (see Foot bar).

### Task stack

The left rail groups the active project's tasks — **Needs you**, **Running**, **Queued**,
**Done** (with a **Load earlier** button paging further into history) — from
`GET /api/projects/{slug}/tasks/stack`. **Needs you** and **Queued** are omitted
entirely when empty, reappearing on the next poll as soon as they have rows.
**Running** and **Done** always stay visible, with a dash placeholder when empty
and a count of `0` in the header — Running as the always-on "nothing running"
signal, Done since it's the paginated group users expect to keep finding in the
same place. Done has no age cutoff — it shows every completed task, newest
first, a page (default 25, max 100 via `page_size`) at a time — and its opaque
`cursor` is stable across requests, keyed on completion time plus task ID so
entries with the same completion timestamp neither repeat nor drop across pages.

Clicking the **Done** header (or pressing Enter/Space on it) collapses its rows,
leaving the header and count visible; the caret rotates to show state. The
collapsed state persists to `localStorage` (falling back to expanded if storage
throws) and survives poll re-renders. Collapsed Done rows drop out of `j`/`k`
navigation order, and selection moves off a Done row if its group collapses
while selected; opening a Done task directly by URL re-expands the group.

A **Running** row's elapsed time measures the current retry's current step — the most
recent of the task's active run start, its most recently (re)dispatched active child run,
and the currently executing step — restarting at zero on every retry rather than growing
across the whole task; the cumulative time since the task's first attempt is still
available as the row's tooltip.

A run waiting at a `poll` step (state `waiting`) stays in **Running** rather than getting
a group of its own — its row's step label reads `waiting · poll <step> · last poll <n>
ago · <count> polls` (degrading to `waiting · poll <step>`, or bare `waiting`, if poll
bookkeeping or the step name isn't available) in place of the usual current-step name.

It polls every few seconds using conditional GET
(`ETag`/`If-None-Match`), so a poll with nothing new costs a 304 rather than a re-render;
when something *has* changed, only the affected rows update in place.

Click a row (or select it with `j`/`k` and press Enter) to open it in the centre pane.

### Centre pane

Shows the selected task's full detail:

- **Header** — task ID, a state pill (running / needs you / queued / succeeded / failed /
  parked — parked also shows how long the run has been parked, e.g. "parked · 12m"; a run
  waiting at a `poll` step shows "waiting" instead of "running"), the
  title, and state-dependent actions: running (including waiting) → Console (raw container output), Workflow
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
- **Facts row** — a merged key/value block capped at two rows regardless of how many facts
  a run has. The lead row is labeled "run" (value: the top-level run ID) unless the task
  has more than one attempt, in which case it's labeled "attempt" and its value leads with
  a group of attempt chips (attempt number + short run ID; outcome and duration are in the
  chip's tooltip; the current attempt and any failed attempts are colored distinctly;
  `Shift+[` and `Shift+]` switch between them). Either way the lead row also carries child
  run IDs, container ID and state, token usage per agent, and the prompt file and git
  revision used. A second "status" row folds in the retry reason (on a retried attempt),
  error message, and timing. Backed by `GET /api/projects/{slug}/tasks/{taskId}/attempts`
  for the attempt list and `GET /api/runs/{id}` for the selected attempt's detail.
- **Step strip** — one horizontal segment per step of the top-level run, with a spawned
  child run's steps inlined immediately after the workflow step that spawned them (the
  flattened-run tree already used by `GET /api/runs/{id}`) — the parent segment gets a `↳`
  marker and its children render shaded and indented in the same strip. Only one segment is
  "focal" — the running step if one is live, else the first failed step — with a filled
  background and a permanently visible result dot and duration; every other segment shows
  just its dot and name, with the duration surfaced via a native tooltip and revealed
  in-place on hover/focus. Poll steps also show their last poll time and count. The live
  (or selected) segment is highlighted. Clicking a segment scopes the log below to that
  step: a client-side filter over the already-streaming lines, matched by `run_id` +
  `step_name` (every SSE line carries the id of the run that published it, so a step name
  shared between a host run and a container run it dispatched isn't ambiguous); scoping a
  workflow step that spawned a child run (e.g. "develop") also includes every line
  published under that child run's id, regardless of which of its steps produced them. Only
  when the stream has no lines for the scope yet (a completed step whose broadcast history
  was already cleared, or a step reconnected to after a page reload) does it fall back to
  fetching the archived output (`GET /api/runs/{id}/steps/{step}/output`); once fetched,
  the archive stays merged ahead of the live stream rather than replacing it. Clicking the
  segment again clears the scope.
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

For a multi-repo project (see Repo sub-tabs above), a repo segment can appear between the
project slug and the task ID: `/{project-slug}/{repo}/{task-id}`. Omitting the repo segment
is a synonym for "all repos" — `/{project-slug}` and `/{project-slug}/all` are equivalent,
and `/{project-slug}/{task-id}` (the legacy shape) keeps working unchanged, since a segment
is only ever read as a repo when it exactly matches one of the project's configured repo
names. `/{project-slug}/{repo}` alone is a bookmarkable link to that repo's scoped stack
with no task selected.

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
| `[` / `]` | Scope the log to the previous / next step, crossing into child-run steps and wrapping at the ends |
| `Shift+[` / `Shift+]` | Switch to the previous / next attempt |
| `g` / `G` | Scroll the log to the top / bottom |
| `f` | Toggle following the live log to its newest line |
| `r` | Release your claim on the open needs-you task (when available); otherwise cycles the repo sub-tabs (multi-repo projects only) |
| `Shift+R` | Jump straight to the "all repos" sub-tab (multi-repo projects only) |
| `x` | Close the open needs-you task in the tracker (when available) |
| `w` | Open the Workflows view |
| `i` | Open the Requirements view |
| `c` | Open the Containers view, or toggle the needs-you compare view / single-attempt log when one is open |
| `d` | Toggle compact / comfortable density |
| `?` | Toggle the keyboard shortcuts overlay |

Shortcuts only fire on bare keys (or Shift chords, e.g. `Shift+[`, `Shift+R`). Any
Cmd/Ctrl/Alt chord is left untouched so the browser or OS handles it instead.

### Foot bar

Shows a one-line activity ticker on the left and a short, contextual set of key hints in
the middle — the ticker is the only real content, so it gets the width; the hints are
chrome that changes with the selected task's state rather than always listing every
shortcut (the full list stays behind `?`). A running task shows `j`/`k`, `[`/`]` step,
`⇧[`/`⇧]` attempt, `tab`, `f`, `a`; a needs-you task shows `j`/`k`, `[`/`]` step, `⇧[`/`⇧]`
attempt, and whichever of `r`/`x` its attention item actually offers; anything else falls
back to a short baseline. `g`/`G` and the log type filter live in the log bar instead,
since they act on the log pane, not the task. The daemon's version sits to the right of
the key hints, baked into the page at render time (it doesn't change during the page's
lifetime, so it renders once at boot rather than polling).

A **density** toggle button sits at the right of the foot bar, next to the key hints,
switching the console between "Comfortable" (default) and "Compact" spacing/font size —
also bound to the `d` key. The choice persists across sessions in `localStorage`
(`cloche:density`), falling back to Comfortable whenever storage is unavailable (e.g.
private browsing).

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
stack's "Done" group uses for its own cursor.

### Secondary views: Workflows, Requirements, Containers, Ledger

A **Tools ▾** button in the tab bar opens a menu folding Workflows, Requirements,
Containers, and Ledger behind one control (the same open/close pattern as the idle-projects
**More** menu); their keyboard shortcuts (`w` / `i` / `c` / `l`) still open each view
directly, independent of the menu. Each opens a project-scoped view as an overlay on top
of the console shell — none of them navigate away from the current project/task URL.
`Esc` closes the topmost open drawer first, then the view itself, before falling back to
the stack-deselect behavior described above.

- **Workflows** — the read-only DAG of steps/wires for the active project's container and
  host workflows (`GET /api/projects/{slug}/workflows`), with location/workflow tabs when
  more than one applies. Built-in workflows are marked with a `built-in` badge. Clicking a
  step node opens a drawer with its type, results, and whichever of the DSL's real config
  keys it uses (`prompt`, `run`, `poll`, `interval`, `agent`, `agent_command`, `agent_args`,
  `intent_tracking`, `workflow_name`, `max_attempts`), plus the referenced prompt/script
  content (`GET /api/projects/{slug}/workflows/{workflow}/steps/{step}/content`).
- **Requirements** — the requirements table, its edit drawer, and the domain editor, driven by
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

The **Ledger** entry in the Tools menu (or pressing `l`) opens a per-project overlay
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
touched it as of dispatch. Attempts that predate this recording are backfilled once by
a bounded-parallelism background job at daemon start
(`sqlite.Store.RunLedgerPromptRevisionBackfill`, gated via `_migrations` so it resumes
across restarts instead of redoing finished projects) rather than on this request path;
while that sweep hasn't reached a project yet, the response reports what's recorded so
far plus `backfill_pending: true` instead of blocking. Per-file `git log --follow`
history is cached keyed on the repo's current HEAD (`promptrev.HistoryCache`), so a
request only ever shells out again after a new commit. Escape or `l` closes the overlay.

---

## JSON API

The dashboard's JSON endpoints remain stable and are also used by the CLI
(`cloche health`, `cloche tasks`, etc.):

- `GET /api/projects` — per-project health, active-run count, attention count
  (`attention_count`/`attention_computed_at`, read from the background attention cache —
  see `internal/attention.Cache` — rather than computed per request), and `loop_running`/
  `latest_run_at` (used by the tab bar's fold rule and the "/" landing-project fallback).
  Also includes the synthetic `system` project (see Tab bar above) once any project-less
  task or run exists; omitted otherwise, and always omitted when the request passes
  `?project=`. `repositories: [{name, path}]` lists every `[[repositories]]` entry declared
  for the project (omitted for a legacy project); the console only renders the repo
  sub-tab row when there is more than one.
- `GET /api/projects/{name}/attention` — the full "Needs you" item list for one project
  plus `computed_at`, from the same cache; `computed_at` is empty for a project that
  hasn't been refreshed yet.
- `GET /api/projects/{name}/tasks` — the orchestration loop's live task snapshot.
- `GET /api/projects/{name}/tasks/stack` — the grouped, bounded task stack (see above).
  Each entry carries a `repository` (the `RepositoryConfig.Name` its run was dispatched
  against; omitted for legacy runs). `?repo=<name>` (or `?repo=all`/omitted) scopes every
  group to one repo; `repo_counts` always reports the full, unscoped per-repo aggregate
  (`needs_you`/`running`/`queued`/`done` counts) regardless of the current `?repo=` scope,
  so the console can badge every repo sub-tab from one response.
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
- `GET /api/runs/{id}`, `GET /api/runs/{id}/stream` — run detail (steps, child runs,
  container state, per-agent token totals, prompt file/git revision) and live SSE log
  streaming.
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
- `GET /api/activity` — the activity ticker/stream (see Foot bar above), filtered by
  `?project=<slug>` (all projects when omitted; `system` for just the synthetic System
  project) and `?failures_only=1`, paged via `?before=<cursor>&limit=<n>`.
- `GET /api/projects/{name}/intent/...` — intent requirements, domains, and scan control.
- `GET /api/projects/{name}/workflows`, `GET .../workflows/{workflow}/steps/{step}/content` —
  workflow structure and step content for the Workflows view's DAG and drawer.
- `GET /api/projects/{name}/containers`, `DELETE .../containers`, `DELETE /api/runs/{id}/container` —
  per-task retained-container listing (with size/age), project-scoped clean-up, and
  per-container delete for the Containers view.
- `GET /api/projects/{name}/ledger` — pass rate over time, mean attempts/tokens to
  success, per-prompt-file revision outcomes, and requirement-injection
  cross-references (see Ledger above); `backfill_pending: true` while the historical
  prompt-revision backfill hasn't reached this project yet.

These are unchanged by the console-shell rework; only the HTML pages that used to render
around them were replaced.
