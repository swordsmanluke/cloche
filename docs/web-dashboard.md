# Web Dashboard

The web dashboard is a browser UI for monitoring and managing Cloche runs. It gives you
live log streaming, workflow visualization, token usage metrics, task management, and
container controls — all without needing the CLI.

## Enabling the Dashboard

Set the `CLOCHE_HTTP` environment variable when starting `cloched`:

```
CLOCHE_HTTP=localhost:8080 cloched
```

Then open `http://localhost:8080` in your browser. The dashboard is not started unless
`CLOCHE_HTTP` is set. Choose any available port.

For a specific interface (e.g. only accessible from the local machine):

```
CLOCHE_HTTP=127.0.0.1:8080 cloched
```

The CLI commands `cloche tasks` and `cloche health` also require `CLOCHE_HTTP` to be set,
since they talk to the daemon's HTTP API.

---

## Pages

### Projects (`/`)

The landing page shows a card for each registered project. Each card displays:

- A **health dot** (green = healthy, yellow = degraded, red = failing) based on recent
  pass/fail rates.
- **Pass stats** — how many of the last 10 runs succeeded.
- **Active run count** — runs currently in progress.
- **Run history dots** — a mini visual history of recent run outcomes.
- A **View Runs** link to the filtered runs list for that project.

Click a project card to go to the Project Detail page.

---

### Project Detail (`/projects/{name}`)

A four-panel page for a specific project.

#### Project Info

Shows the Docker image (parsed from the project's `Dockerfile`), the project version
(from `.cloche/version`), and all prompt files under `.cloche/prompts/`. For each prompt
file you can:

- **Expand the file** to read its current content.
- **View git history** — a list of commits that touched the file, with short SHA, date,
  and message.
- **Click a commit** to load an inline unified diff showing exactly what changed.

#### Token Burn

Shows token usage data for the project:

- **1-hour burn rate** — tokens consumed per hour across all agents in the last hour.
- **24-hour totals** — total input and output tokens per agent over the last 24 hours.

Each row shows the agent name (e.g. `claude`), input tokens, output tokens, and combined
total. Values are formatted with K/M suffixes for readability (e.g. `1.5M`).

#### Tasks

Shows the task pipeline for the project's orchestration loop. For each discovered task
you can see the task ID, title, current status (`open`, `in-progress`, `closed`), whether
it is assigned to a run, and a link to the assigned run if one is active.

**Releasing a stale task:** If a task is stuck in `in-progress` with no active run — for
example after the daemon restarted or a container was lost — a **Release** button appears
next to it. Clicking Release triggers the project's `release-task` workflow to return the
task to `open` status in your tracker. (Requires a `release-task` workflow to be defined;
see [USAGE.md](USAGE.md) for details.)

The task list refreshes automatically every few seconds.

#### Workflow DAG

A visual graph of the project's workflows. If the project has both container and host
workflows, tabs let you switch between them. If a location has multiple workflows,
additional tabs let you select which one to view.

The graph shows:

- **Nodes** — each step, labeled with its name and a type icon (🤖 agent, 📜 script,
  🔁 workflow dispatch).
- **Edges** — wires between steps, labeled with the result name (e.g. `success`, `fail`).
  Colors: green for success results, red for failure results, other colors for custom
  result names.
- **Terminal nodes** — `done` and `abort` are rendered distinctly and positioned at the
  bottom of the graph.

Click any step node to open a **drawer panel** on the right side with:

- The step's name, type, and declared results.
- `max_attempts` if set.
- For **agent steps**: the full content of the prompt file.
- For **script steps**: the shell command or the contents of the script file.
- For **workflow steps**: the name of the dispatched container workflow.

Press Escape or click the X button to close the drawer.

The graph scrolls horizontally and vertically for large workflows.

---

### Runs (`/runs` or `/projects/{name}/runs`)

The runs list groups workflow executions by task and attempt. Each group shows:

- A **task header** with the task ID, title, and overall status.
- **Attempt blocks** under each task — one per attempt (e.g. `Attempt a12z`), with a
  timestamp and aggregate status. The latest attempt is expanded; earlier ones are
  collapsed.
- **Run rows** within each attempt — the top-level run (e.g. `main`) and any child runs
  it dispatched (e.g. `develop`, `finalize`).

Click an attempt header to expand or collapse it. Accordion state is preserved across
automatic page refreshes.

Runs with no associated task appear below the task groups as ungrouped entries.

Use the **project filter** dropdown at the top to limit the view to a specific project.

---

### Run Detail (`/runs/{id}`)

Shows the full detail of a single workflow execution.

#### Steps Table

A table of all steps in the run, with name, result, duration, and (for agent steps)
token usage. Click any row to expand an output panel showing the captured log for that
step. The output panel streams live during active steps.

#### Log Viewer

A scrollable log panel at the bottom of the page streams all output for the run in real
time via [SSE](https://developer.mozilla.org/en-US/docs/Web/API/Server-sent_events). Each
line is labeled with its type (script output, LLM conversation, status event).

For completed runs, all captured output is loaded immediately. For active runs, existing
output is replayed first and then new lines are appended as they arrive.

Use **Load Earlier** to fetch older log lines when there is history before the current
view.

#### Container Management

Shows the state of the Docker container for this run:

| State | Meaning |
|-------|---------|
| Container running | The run is active. |
| Container stopped | The run ended but the container is retained. |
| Container available | Container is retained and can be inspected. |
| Container removed | The container has been deleted. |

A **Delete container** button appears when the container is retained (stopped or
available). Use it to free disk space once you no longer need the container.

#### Cancelling a Run

A **Cancel** button appears at the top of the page for runs that are pending or actively
running. Clicking it stops the run and marks it cancelled.

---

### Task Detail (`/tasks/{taskID}`)

Shows all attempts for a task, newest first. Each attempt row shows the attempt ID,
start time, duration, and result. Click an attempt to expand a list of the workflow runs
and steps within it. From here you can follow links to individual run detail pages for
full log output.

---

## Features at a Glance

| Feature | Where |
|---------|-------|
| Live log streaming (SSE) | Run detail → Log viewer, Step output panels |
| Workflow DAG visualization | Project detail → Workflow DAG tab |
| Token burn metrics | Project detail → Token Burn panel |
| Task pipeline & release | Project detail → Tasks panel |
| Container delete | Run detail → Container Management |
| Prompt diff viewer | Project detail → Project Info → commit history |
| Run cancel | Run detail → Cancel button |
| Task/attempt grouping | Runs list |
| Project health overview | Projects landing page |
