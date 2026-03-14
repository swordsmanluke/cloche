# Project Pages Design

**Date:** 2026-03-06
**Status:** Design

## Problem

The projects landing page shows only health dots and a "View Runs" link. There is no
dedicated project page. Useful project-level information and controls are either missing
or buried in other views.

## Goal

Add a dedicated `/projects/{name}` page with four panels:

1. **Orchestrator control** — view status, enable/disable the auto-poll loop, override concurrency
2. **Project info** — Docker image, prompt file evolution history, project version
3. **Tasks** — live task pipeline view showing discovered tasks, their status, and assigned runs
4. **Workflow view** — read-only DAG of steps/wires, drillable to prompt/script content

## Route

```
GET /projects/{name}
```

`{name}` is the project label (same string used in `/api/projects/{name}/trigger`).
The handler resolves the label back to a project directory via `projectLabels()`.

Project cards on the landing page link to `/projects/{name}`. The project page
includes a compact recent-runs strip (last 10 dots) and a "View all runs" link to
`/projects/{name}/runs` for the filtered runs list.

## Panel 1: Orchestrator Control

### Display
- Status line: `Enabled / Disabled`, in-flight run count, concurrency limit
- Toggle button: **Enable** / **Disable** (controls the auto-poll loop for this project)
- Concurrency override: number input + **Apply** button (runtime only, resets on daemon restart)

### API additions

```
GET  /api/projects/{name}/orchestrator
POST /api/projects/{name}/orchestrator/start
POST /api/projects/{name}/orchestrator/stop
POST /api/projects/{name}/orchestrator/concurrency   body: {"concurrency": N}
```

`GET` response:
```json
{
  "enabled": true,
  "in_flight": 1,
  "concurrency": 2
}
```

The orchestrator currently has no per-project enable/disable concept. We add an
`enabled` flag to `ProjectConfig` (default true when registered). Start/stop toggle
this flag. When disabled, `Run()` returns 0 without dispatching.

Concurrency override updates `ProjectConfig.Concurrency` in memory only.

The Handler needs a reference to the `*orchestrator.Orchestrator` (or a minimal
interface exposing `Status(dir)`, `SetEnabled(dir, bool)`, `SetConcurrency(dir, int)`,
`InFlight(dir)`). Wire this in via a `HandlerOption`.

## Panel 2: Project Info

### Display
- **Docker image**: read the `FROM` line(s) from `.cloche/Dockerfile`. Show the last
  non-builder stage's base image (i.e. the stage that produces the final image).
- **Project version**: integer, sourced from `.cloche/version` (see Project Versioning
  design). Display as `v3`.
- **Prompt files**: for each file under `.cloche/prompts/`, show:
  - Current file contents in a collapsible accordion
  - Git commit history (short SHA, date, message)
  - Click a commit entry to expand and show a unified diff inline

### API additions

```
GET /api/projects/{name}/info
```

Response:
```json
{
  "docker_image": "ubuntu:24.04",
  "version": 3,
  "prompt_files": [
    {
      "path": ".cloche/prompts/implement.md",
      "content": "Full text of the prompt file...",
      "history": [
        {"sha": "abc1234", "date": "2026-03-05", "message": "evolution: tighten scope"},
        {"sha": "def5678", "date": "2026-03-01", "message": "init"}
      ]
    }
  ]
}
```

Implementation: run `git log --follow --oneline -- <file>` in the project directory
via `exec.Command`. Diff for a specific commit loaded lazily on click via:

```
GET /api/projects/{name}/info/prompt-diff?file=.cloche/prompts/implement.md&sha=abc1234
```

Returns plain text unified diff (`git show <sha> -- <file>`).

## Panel 3: Tasks

### Display
- Table showing discovered tasks: Task ID, Title, Status, Assigned (yes/no), Run link
- Status badges: `open` (running style), `closed` (succeeded style), other (pending style)
- Auto-refreshes every 5 seconds via polling
- Shows "No tasks discovered" when the project has no list-tasks output

### API

```
GET /api/projects/{name}/tasks
```

Returns the task pipeline state for the project's orchestration loop.

### Runs page changes

- `list-tasks` workflow runs are hidden from the Runs page (both server-rendered and
  JS-rendered views)
- Runs with the same `task_id` are grouped under a task header row
- The task header shows an aggregate status badge (active statuses running/pending outweigh terminal ones; among terminal-only runs, host runs take precedence over child container runs since the host run reflects the full attempt outcome including finalize; if no host runs exist the most recently started determines the result)
- The `task_id` and `task_status` fields are included in the `/api/runs` JSON response
- Runs without a `task_id` appear ungrouped below task groups

## Panel 4: Workflow View

### Display
- Two-level tab navigation:
  - **Location tabs** (Container / Host) shown when both container and host workflows exist
  - **Workflow tabs** shown when the active location has multiple workflows
- Per workflow: a read-only DAG rendered client-side
  - Nodes = steps, rendered as boxes showing step name + type icon (agent 🤖 / script 📜 / workflow 🔁)
  - Edges = wires, labeled with the result name (e.g. `success`, `fail`)
  - Terminal nodes `done` and `abort` rendered distinctly
- Clicking a step node opens a right-side **drawer panel** (CSS slide-in):
  - Step name and type
  - Results list
  - `max_attempts` if set
  - For agent steps: full content of the referenced prompt file (loaded on demand)
  - For script steps: the `command`/`script` string or contents of the referenced script file
  - For host script steps: contents of a referenced script file under `.cloche/scripts/` (text files only); the content panel is hidden when no displayable content is available
  - For workflow steps: displays the dispatched workflow name
  - Drawer closes via X button or Escape, returning focus to the DAG without page reload

### DAG rendering
Client-side Sugiyama-style layered layout with no external library. Steps are assigned to
layers via longest-path from sources, then ordered within each layer using barycenter
heuristic (4 forward/backward passes) to minimize edge crossings. Layout flows
top-to-bottom with SVG Bezier curve edges.

Wires are colorized by result type: green for success results (`success`, `ok`, `done`,
`pass`), red for failure results (`failed`, `fail`, `error`), then blue, yellow, magenta,
cyan, and orange for other result names. Each color gets a matching SVG arrowhead marker.

Success wires between steps use straight Bezier curves along the main vertical flow.
Non-success wires between non-terminal steps are offset horizontally (35px) so they don't
overlap the happy-path edges.

Non-success wires targeting terminals (`done` or `abort`) are routed through **wire
columns** — vertical channels positioned to the right of the main graph. Each unique
(terminal, result) pair gets its own column. Source nodes connect to their wire column via
a horizontal segment with a small link-node dot at the junction, then the column runs
vertically down to the terminal node (curving in if the column is offset from the
terminal center). This isolates failure paths from the main flow and avoids overlapping
edges as graphs grow in complexity. Terminals that receive only non-success wires are
repositioned horizontally under their wire columns.

The DAG container scrolls horizontally and vertically when the graph exceeds the panel
dimensions.

### API additions

```
GET /api/projects/{name}/workflows
```

Response:
```json
[
  {
    "name": "develop",
    "file": ".cloche/develop.cloche",
    "location": "container",
    "steps": [
      {
        "name": "implement",
        "type": "agent",
        "results": ["success", "fail"],
        "config": {"prompt": ".cloche/prompts/implement.md", "max_attempts": null}
      },
      ...
    ],
    "wires": [
      {"from": "implement", "result": "success", "to": "test"},
      ...
    ],
    "entry_step": "implement"
  }
]
```

The `location` field is either `"container"` or `"host"`. Host workflows are parsed from
`.cloche/host.cloche` and included in the same response.

```
GET /api/projects/{name}/workflows/{workflow}/steps/{step}/content
```

Returns the raw content of the prompt file or script for that step (plain text).
For `run` steps, only serves content from text files under `.cloche/scripts/`;
file references outside that directory or binary files return an empty response.
Inline command strings (not file references) return empty unless they reference
a script file under `.cloche/scripts/`.

## Template / file changes

- New template: `templates/project_detail.html`
- New page registered in `NewHandler`: `"project_detail"`
- New routes registered in `NewHandler` mux
- Handler struct gains an optional orchestrator interface field (via `HandlerOption`)

## Not in scope

- Editing workflow files from the UI
- Evolution history of workflow structure (just current state)
- Persisting concurrency overrides to disk
