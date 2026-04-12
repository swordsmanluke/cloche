# Cloche Usage

Cloche runs containerized workflows for autonomous coding agents. You define a
workflow graph in the `.cloche` DSL, Cloche copies your project into a Docker
container, runs the workflow steps (agent prompts and shell scripts), and extracts
results to a git branch.

## Core Concepts

- **Workflow**: A directed graph of steps connected by named-result wiring.
- **Step**: A unit of work. Type is inferred: `prompt` = agent step, `run` = script step, `workflow_name` = workflow step.
- **Result**: A named outcome reported by a step (e.g. `success`, `fail`, `needs-research`). Steps declare their possible results; the engine follows wiring based on the reported result.
- **Wiring**: Maps `step:result` pairs to the next step. Separate from step definitions.
- **Terminals**: `done` (success) and `abort` (failure) are built-in terminal targets.

## Workflow DSL

Workflow files use the `.cloche` extension and live in `.cloche/`. The first step
declared is the entry point. Graphs are validated at parse time (all results wired,
no orphans, entry point exists).

### Minimal Example

```
workflow "develop" {
  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test 2>&1"
    results = [success, fail]
  }

  implement:success -> test
  implement:fail -> abort
  test:success -> done
  test:fail -> abort
}
```

### Step Configuration

| Key | Type | Description |
|-----|------|-------------|
| `prompt` | string or `file("path")` | Prompt template. Makes this an agent step. |
| `run` | string | Shell command. Makes this a script step. |
| `workflow_name` | string | Workflow to dispatch by name. Makes this a workflow step. Available in both host and container workflows. |
| `results` | ident list | Declared result names, e.g. `[success, fail, give-up]`. |
| `max_attempts` | integer | Max retries before automatic `give-up` result, e.g. `2`. |
| `timeout` | string | Step timeout as Go duration, e.g. `"30m"`, `"2h"`. Default: 30m. |
| `agent_command` | string | Agent binary name(s), comma-separated for fallback chains, e.g. `"claude,gemini"`. |
| `agent_args` | string | Override default agent arguments. |
| `agent` | identifier | Reference a named agent declared in the workflow's `agent` block. Expands into `agent_command` and `agent_args`. Step-level `agent_command`/`agent_args` still override it. |
| `feedback` | string | Set to `"true"` to include `.cloche/output/*.log` content in the prompt. |
| `usage_command` | string | Shell command to run after an agent step completes to capture token usage. Output must be JSON: `{"input_tokens": N, "output_tokens": N}`. If absent or the command fails, usage is not tracked. Overrides any adapter-level default (e.g. from `[agents.codex]` in `config.toml`). |
| `prompt_step` | string | For workflow steps: which preceding step's output to use as the prompt. |

A step must have exactly one of `prompt`, `run`, or `workflow_name`.

### The `file()` Function

`file("path")` reads the file at the given path relative to the working directory
(`/workspace/` in containers) at execution time, not parse time. Use it for prompt
templates:

```
prompt = file(".cloche/prompts/implement.md")
```

### The `container {}` Block

Can appear at step level or workflow level. Keys are stored with a `container.` prefix.

**Step level** — per-step container config:
```
step code {
  prompt = file(".cloche/prompts/implement.md")
  container {
    image         = "custom-agent:v2"
    memory        = "4g"
    network_allow = ["docs.python.org", "api.example.com"]
  }
  results = [success, fail]
}
```

**Workflow level** — defaults for all steps:
```
workflow "develop" {
  container {
    agent_command = "gemini"
    image         = "myregistry/myimage:v2"
    memory        = "4g"
  }
  ...
}
```

Supported keys: `id`, `image`, `memory`, `network_allow`, `agent_command`, `agent_args`.

> **Note:** `network_allow` and `memory` are parsed and stored but not yet enforced at runtime —
> containers currently run with unrestricted network access and no memory limit. Declaring them in
> your workflow documents intent and will take effect when enforcement is implemented.

### The `host {}` Block

Declares a workflow as a host workflow. Can appear in any `.cloche` file. Keys are stored
with a `host.` prefix. Configures agent defaults for agent steps running on the host.
An empty `host {}` block (no keys) is valid and simply marks the workflow as host-side.

```
workflow "main" {
  host {
    agent_command = "claude"
  }
  ...
}
```

Supported keys: `agent_command`, `agent_args`. Step-level `agent_command` and
`agent_args` config keys override the workflow-level `host {}` defaults.

### Agent Declarations

Workflows can declare named agents at the workflow level. An `agent` block names a
reusable agent configuration so that multiple prompt steps can reference it without
repeating the command and arguments each time.

**Syntax:**

```
workflow "develop" {
  agent haiku_claude {
    command = "claude"
    args    = "-p --dangerously-skip-permissions --model claude-haiku-4-5"
  }

  agent codex {
    command = "codex"
    args    = "--full-auto"
  }

  step implement {
    prompt  = file(".cloche/prompts/implement.md")
    agent   = haiku_claude
    results = [success, fail]
  }

  step review {
    prompt  = file(".cloche/prompts/review.md")
    agent   = codex
    results = [success, fail]
  }

  implement:success -> review
  implement:fail    -> abort
  review:success    -> done
  review:fail       -> implement
}
```

**Agent block fields:**

| Field | Required | Description |
|-------|----------|-------------|
| `command` | yes | The agent binary to run (e.g. `claude`, `codex`, `ollama`). |
| `args` | no | Arguments passed to the agent command. |

**Using an agent in a step:** Set `agent = <identifier>` on any prompt (agent-type)
step. The identifier must match a name declared in an `agent` block in the same
workflow. The `agent` key is not valid on script or workflow steps.

**Resolution order:** `agent = <identifier>` expands the declaration's `command` and
`args` into the step's `agent_command` and `agent_args`. The full resolution order
from highest to lowest priority is:

1. Step-level `agent_command` / `agent_args`
2. `agent = <identifier>` declaration
3. Workflow-level block (`container { agent_command }` or `host { agent_command }`)
4. `CLOCHE_AGENT_COMMAND` environment variable
5. Default: `claude`

**Validation rules:**
- Referencing an undeclared agent identifier is a **validation error**.
- Only prompt (agent-type) steps may use the `agent` key — using it on a script or
  workflow step is a **validation error**.
- Duplicate agent names within a workflow are a **parse error**.
- An `agent` block without a `command` field is a **parse error**.

See also: [docs/workflows.md](workflows.md) for full DSL grammar details.

### Wiring Syntax

Connect steps with `step:result -> next_step`:

```
implement:success -> test
implement:fail -> abort
test:success -> done
test:fail -> fix
```

### Wire Output Mappings

Extract values from a step's JSON output and inject as env vars into the target:

```
step-a:success -> step-b [ ENV_VAR = output.field, OTHER = output.list[0].name ]
```

Path expressions:

| Expression | Meaning |
|---|---|
| `output` | Raw output (full string) |
| `output.key` | JSON object field access |
| `output[N]` | JSON array index (0-based) |
| `output.a.b.c` | Deeply nested field access |
| `output.items[0].name` | Mixed field and index chaining |

If the output is valid JSON, path expressions navigate the structure. If it's plain
text, only bare `output` works.

### Retry Loops

Wire failures back to earlier steps:

```
test:fail -> fix
fix:success -> test    // retry the test
fix:fail -> abort
```

Use `max_attempts` to cap retries. When exhausted, the step returns `give-up`:

```
step fix {
  prompt = file(".cloche/prompts/fix.md")
  max_attempts = 2
  results = [success, fail, give-up]
}
```

### Parallel Branches (Fanout)

Wire one result to multiple targets for concurrent execution:

```
test:success -> lint
test:success -> quality
```

### Collect (Join)

Synchronize parallel branches:

```
collect all(lint:success, quality:success) -> done
collect any(lint:success, quality:success) -> done
```

`all` fires when every condition is met. `any` fires when at least one is.

### Comments

Line comments with `//`:

```
// This is a comment
implement:success -> test  // inline comment
```

## Result Protocol

Steps report results by printing a marker line to stdout:

```
CLOCHE_RESULT:<name>
```

For example: `CLOCHE_RESULT:success`, `CLOCHE_RESULT:needs-research`, `CLOCHE_RESULT:give-up`.

Rules:
- The **last** `CLOCHE_RESULT:` line wins if multiple are printed.
- Marker lines are **stripped** from captured output (not passed to logs or downstream steps).
- The result name must match one of the step's declared `results`.
- For script steps with no marker: exit 0 = `success`, exit non-zero = `fail`.
- For agent steps: if exit non-zero with a marker, the marker result is used. If exit non-zero without a marker, falls back to the next agent in the fallback chain (or returns `fail` if last).

## Prompt Assembly

When an agent step runs, Cloche assembles a prompt from these sections (joined by blank lines):

1. **Step template**: The step's `prompt` content (inline string or resolved `file("path")`).
2. **User request**: Content of `.cloche/<run-id>/prompt.txt` (set via `--prompt` flag), prefixed with `## User Request`.
3. **Validation output** (opt-in): If `feedback = "true"`, reads all `.log` files from `.cloche/output/` and includes them prefixed with `## Validation Output`.
4. **Result selection**: Lists the step's declared results with instructions to print exactly one `CLOCHE_RESULT:<name>` marker.

The assembled prompt is passed to the agent command via stdin.

## Agent Command Resolution

Priority (highest to lowest):
1. Step-level `agent_command`
2. Workflow-level config block (`container { agent_command }` for container workflows, `host { agent_command }` for host workflows)
3. `CLOCHE_AGENT_COMMAND` environment variable
4. Default: `claude`

### Fallback Chains

Comma-separated commands are tried in order:

```
agent_command = "claude,gemini,codex"
```

- **Command not found / failed to start** — try next command
- **Exit non-zero without `CLOCHE_RESULT` marker** — try next command
- **Exit non-zero with `CLOCHE_RESULT` marker** — use that result (no fallback)
- **Exit 0** — use result (no fallback)
- **All commands fail to start** — step returns an error
- **Last command crashes without marker** — step returns `fail`

Known agents (e.g. `claude`) get default arguments automatically. Unknown agents receive the prompt on stdin with no flags. Some default arguments are overridable via `agent_args`; others are required and always injected. See [Built-in Agents](built-in-agents.md) for the full argument reference.

### Agent Setup Guides

For detailed container setup instructions per agent:

- [How to set up Claude Code](agent-setup-claude.md) — session-based auth, no API key needed
- [How to set up Codex](agent-setup-codex.md) — API key configuration

## Workflow Locations

**Container workflows** (the default) run inside Docker via `cloche-agent`. Steps may
be `agent`, `script`, or `workflow` type.

**Host workflows** (declared with a `host { }` block) run on the host machine as the
daemon. Any `.cloche` file can contain host workflows. Steps may be `agent`, `script`,
or `workflow` type. The `workflow_name` step type dispatches a named workflow run
and blocks until it completes. Script steps
execute with their working directory set to the main git worktree, so host-workflow
fixes on main are available to in-flight runs even if they branched earlier.

A single file may contain **multiple named workflows**. The daemon uses up to two
host workflow phases for orchestration:

| Phase | Workflow name | Purpose |
|-------|--------------|---------|
| 1 | `list-tasks` | Discover available work. Output is JSONL (one task per line). |
| 2 | `main` | Do the work. Receives a task ID via `CLOCHE_TASK_ID` env var. |

Additionally, a **`release-task`** host workflow may be defined. This is not part of the
automatic orchestration loop — it is invoked on demand (e.g. from the
web dashboard) to release a stale claimed task back to `open` status. A task is
considered stale when it has `in_progress` status but no active worker is running for
it (e.g. after a failed run or daemon restart). The workflow receives `CLOCHE_TASK_ID`.

Only `main` is required. If `list-tasks` is absent, the daemon uses a single-workflow
mode.

### list-tasks Output Format

The `list-tasks` workflow's final step output is parsed as JSONL. Each line is a JSON
object:

| Field | Required | Description |
|-------|----------|-------------|
| `id` | yes | Unique task identifier |
| `status` | yes | One of `open`, `closed`, `in-progress` |
| `title` | no | Short summary |
| `description` | no | Full description |
| `metadata` | no | Arbitrary key-value pairs |

The daemon picks the first task with status `open` and runs `main` with that task's
ID set in `CLOCHE_TASK_ID`. Tasks are deduplicated within a timeout window to prevent
rapid reassignment of the same task.

### Host Workflow Example

A two-phase host workflow setup (this is the default scaffold generated by `cloche init`):

```
workflow "list-tasks" {
  host {}

  step get-tasks {
    run     = "python3 .cloche/scripts/get-tasks.py"
    results = [success, fail]
  }

  get-tasks:success -> done
  get-tasks:fail    -> abort
}

workflow "main" {
  host {}

  step claim-task {
    run     = "python3 .cloche/scripts/claim-task.py"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  step finalize {
    run     = "python3 .cloche/scripts/finalize.py"
    results = [success, fail]
  }

  claim-task:success -> develop
  claim-task:fail    -> abort
  develop:success    -> finalize
  develop:fail       -> finalize
  finalize:success   -> done
  finalize:fail      -> abort
}
```

The `list-tasks` script writes JSONL to `$CLOCHE_STEP_OUTPUT`. The `main` workflow
receives the task ID and handles all phases of work including any post-run cleanup.

### Host Step Environment Variables

| Variable | Description |
|----------|-------------|
| `CLOCHE_PROJECT_DIR` | Absolute path to the project directory on the host. |
| `CLOCHE_STEP_OUTPUT` | Path where this step should write its output (for output mappings). |
| `CLOCHE_PREV_OUTPUT` | Path to the output file from the immediately preceding step. |
| `CLOCHE_RUN_ID` | Workflow ID for this workflow execution (e.g. `a133:develop`). |
| `CLOCHE_TASK_ID` | Task ID assigned by the daemon (set for the `main` phase). |
| Wire-mapped vars | Any env vars declared in wire output mappings. |

### Container Environment Variables

| Variable | Description |
|----------|-------------|
| `CLOCHE_RUN_ID` | Workflow ID for this workflow execution (e.g. `a133:develop`). |
| `CLOCHE_TASK_ID` | Task ID assigned by the daemon. Set when the container run is associated with a task. |
| `CLOCHE_ATTEMPT_ID` | Attempt identifier for this container run. Used for unique container naming. |
| `CLOCHE_PROJECT_DIR` | Working directory (set for script steps so `cloche get`/`cloche set` work). |
| `ANTHROPIC_API_KEY` | Passed through from the host if set. |
| `CLOCHE_AGENT_COMMAND` | Overrides the default agent command inside the container. |

## Setting Up Host Workflows

Host workflows are the orchestration layer that ties everything together. When you
run `cloche init`, a two-phase host workflow (`list-tasks`, `main`) is generated along
with Python scripts for task management. You can adapt these scripts to connect to
your task tracker of choice.

The daemon recognizes two host workflow names and runs them in order:

1. **`list-tasks`** — discover available work
2. **`main`** — execute the work (including any post-run cleanup)

An optional workflow, **`release-task`**, can be defined for releasing stale claimed
tasks back to open status. It is not part of the automatic loop — it runs on demand
when triggered from the web dashboard's Release button.

These host workflows can live in any `.cloche` file(s). Only `main` is required;
`list-tasks` and `release-task` are optional.

### `list-tasks` — Discovering Work

The `list-tasks` workflow runs first. Its job is to query your task tracker (issue
tracker, project board, ticket queue, etc.) and output a list of available tasks.

**When it runs:** The daemon calls `list-tasks` at the start of each orchestration
loop iteration, before launching any new `main` runs.

**Output format:** The final step must write JSONL (one JSON object per line) to
`$CLOCHE_STEP_OUTPUT`. Each line represents a task:

```json
{"id": "PROJ-42", "status": "open", "title": "Fix login bug", "description": "Users report..."}
{"id": "PROJ-43", "status": "in-progress", "title": "Add caching", "description": "..."}
{"id": "PROJ-44", "status": "open", "title": "Update docs", "description": "..."}
```

The daemon picks the first task with `status: "open"` and passes it to `main`.
Tasks that were recently assigned are deduplicated within a timeout window (default
5 minutes) to prevent the same task from being picked repeatedly.

**How to configure:** Replace `.cloche/scripts/get-tasks.py` with a script that
queries your task tracker (Linear, Jira, GitHub Issues, etc.) and outputs JSONL:

```
workflow "list-tasks" {
  host {}

  step get-tasks {
    run     = "python3 .cloche/scripts/get-tasks.py"
    results = [success, fail]
  }

  get-tasks:success -> done
  get-tasks:fail    -> abort
}
```

**If omitted:** The daemon falls back to single-workflow mode — it runs `main`
once without task assignment. This is fine for simple use cases where you trigger
runs manually with `cloche run`.

### `main` — Executing Work

The `main` workflow does the actual work. This is the only required host workflow
and is always generated by `cloche init`.

**When it runs:** After `list-tasks` selects a task (or immediately if `list-tasks`
is absent). The daemon sets the `CLOCHE_TASK_ID` environment variable from the
selected task.

**How to configure:** A typical `main` workflow claims the task, then dispatches a
container workflow:

```
workflow "main" {
  host {}

  step claim-task {
    run     = "python3 .cloche/scripts/claim-task.py"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  claim-task:success -> develop
  claim-task:fail    -> abort
  develop:success    -> done
  develop:fail       -> done
}
```

The `workflow_name` step type dispatches a container workflow run and blocks until
it completes. You can add steps before or after the container workflow — for example,
steps to mark the task in-progress in your tracker before the run, or cleanup steps
after it completes.

### `release-task` — Releasing Stale Claims

The `release-task` workflow handles releasing tasks that are stuck in `in_progress`
status without an active worker. This happens when a run fails, the daemon restarts,
or a container is lost. The web dashboard detects these stale tasks and shows a
**Release** button next to them.

**When it runs:** On demand, when a user clicks Release in the web dashboard. The
daemon sets `CLOCHE_TASK_ID` to the task being released.

**How to configure:**

```
workflow "release-task" {
  step release-task {
    run     = "bash .cloche/scripts/release-task.sh"
    results = [success, fail]
  }

  release-task:success -> done
  release-task:fail    -> abort
}
```

A typical release script returns the ticket to `open` status and unassigns the owner
in your task tracker.

**If omitted:** The Release button will not function. Stale tasks must be manually
unclaimed in your task tracker.

### Putting It All Together

The two-phase model enables fully automated task pipelines:

1. The orchestration loop calls `list-tasks` to find open work
2. It picks an open task and launches `main` with the task context
3. `main` prepares a prompt, dispatches a container workflow, and handles cleanup
4. The loop repeats, picking the next open task

Start the loop with `cloche loop` and monitor tasks with `cloche tasks`. The
`--max` flag controls how many `main` runs execute concurrently.

## Complete Example: Container Workflow with Parallel Validation

```
workflow "develop" {
  step implement {
    prompt = file("prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "bundle exec rake test 2>&1"
    results = [success, fail]
  }

  step lint {
    run = "bundle exec rubocop 2>&1"
    results = [success, fail]
  }

  step quality {
    run = "python3 scripts/quality-check.py 2>&1"
    results = [success, fail]
  }

  step fix {
    prompt = file("prompts/fix.md")
    max_attempts = 2
    results = [success, fail, give-up]
  }

  implement:success -> test
  implement:fail -> abort

  test:success -> lint
  test:success -> quality
  test:fail -> fix

  lint:fail -> fix
  quality:fail -> fix
  collect all(lint:success, quality:success) -> done

  fix:success -> test
  fix:fail -> abort
  fix:give-up -> abort
}
```

## Container Isolation Model

- **Files in**: `docker cp` copies the project into `/workspace/`. No bind mounts. Override files from `.cloche/overrides/` are applied on top. `.git/` is included.
- **Files out**: On completion, the daemon extracts results via `docker cp` into a git worktree and commits to a `cloche/<run-id>` branch. If the agent made commits inside the container, their messages are preserved in the squash commit (like `git merge --squash`). When no container commits exist, the commit message includes a file-change summary instead.
- **Auth files**: `~/.claude/` and `~/.claude.json` are copied into each container at `/home/agent/` for Claude Code session reuse. Copied (not bind-mounted) so each container gets its own isolated copy.
- **Network**: Containers have network access (needed for API calls).
- **Cleanup**: Containers are removed after successful runs unless `--keep-container` is set. Failed runs always keep their container.

Your project directory is never modified by the container.

## Token Usage

Cloche tracks token consumption per agent step and exposes aggregate metrics in `cloche
status`, the web dashboard, and a dedicated gRPC endpoint.

### What Is Tracked

Each completed agent step records:

| Metric | Description |
|--------|-------------|
| `input_tokens` | Tokens sent to the agent (prompt) |
| `output_tokens` | Tokens returned by the agent (completion) |
| `agent_name` | Which agent ran the step (e.g. `claude`, `codex`) |

These are stored per step execution and can be aggregated over any time window.

### How Tracking Works Per Agent

**Claude Code** — token usage is extracted automatically from the
`--output-format stream-json` result event. No extra configuration needed.

**Other agents** — use the `usage_command` step config key (or set it globally in
`config.toml` under `[agents.<name>]`) to run a shell command after each agent step.
The command must print JSON to stdout:

```json
{"input_tokens": 1234, "output_tokens": 567}
```

If the command is absent or fails, usage for that step is not tracked. Execution
continues normally regardless.

### Token Usage in `cloche status`

**Overview mode** (`cloche status` with no arguments) shows a per-agent burn rate
section at the bottom if any usage data exists for the last hour:

```
Token usage (last 1h):
  claude     4,521 in / 2,103 out   6,624 total   ~18.2k/hr
  codex      1,200 in /   890 out   2,090 total   ~5.7k/hr
```

Each row shows the agent name, input/output token counts, total tokens, and the
burn rate (total tokens per hour over the last hour). The burn rate uses `~Xk/hr`
notation for values ≥ 1,000 and `~X/hr` for smaller values. The section is omitted
entirely when no usage data is available.

**Task status** (`cloche status <task-id>`) includes a `Tokens` line showing total
consumption across all attempts for that task, broken down by agent:

```
Tokens:  8,714 (claude: 6,624 / codex: 2,090)
```

This line is omitted if no usage data exists for the task.

### Token Usage in the Web Dashboard

The project detail page shows a **Token Usage** panel (auto-hidden if empty) that
refreshes every 30 seconds:

- **Burn rate (last 1h)** — per-agent tokens per hour, formatted as `~Xk/hr` or `~X/hr`
- **Totals (last 24h)** — per-agent input/output/total token counts

The run detail page shows per-step token counts (input/output) in the steps table
for any step that reported usage.

### `GetUsage` gRPC Endpoint

The daemon exposes a `GetUsage` RPC on the `ClocheService` for programmatic access:

```protobuf
rpc GetUsage(GetUsageRequest) returns (GetUsageResponse);

message GetUsageRequest {
  string project_dir    = 1; // empty = global (all projects)
  string agent_name     = 2; // empty = all agents
  int64  window_seconds = 3; // 0 = all time; >0 = seconds back from now
}

message GetUsageResponse {
  repeated UsageSummary summaries = 1;
}

message UsageSummary {
  string agent_name    = 1;
  int64  input_tokens  = 2;
  int64  output_tokens = 3;
  int64  total_tokens  = 4;
  double burn_rate     = 5; // tokens per hour (0 when window_seconds = 0)
}
```

**Filtering:**

| Field | Effect |
|-------|--------|
| `project_dir` | Limit to a single project. Empty returns global totals. |
| `agent_name` | Limit to a single agent. Empty returns all agents (one summary per agent). |
| `window_seconds` | Time window ending now. `0` means no time filter (all-time totals, burn rate is 0). |

**Example:** fetch the last-hour burn rate for all agents in the current project:

```go
resp, err := client.GetUsage(ctx, &pb.GetUsageRequest{
    ProjectDir:    "/path/to/my-project",
    WindowSeconds: 3600,
})
for _, s := range resp.Summaries {
    fmt.Printf("%s: %.0f tokens/hr\n", s.AgentName, s.BurnRate)
}
```

## CLI Reference

Every subcommand supports `--help` (or `-h`) to show detailed usage, flags, and
examples. Use `cloche help <command>` for the same output:

```
cloche --help              # top-level overview
cloche help run            # detailed help for "run"
cloche run --help          # same as above
```

### `cloche init`

Set up or refresh a Cloche project. Safe to run on any project, including ones
already using Cloche — it will not overwrite existing files.

```
cloche init [-n | --new] [--install-shell-helpers]
            [--workflow <name>] [--base-image <image>]
            [--agent-command <cmd>] [--no-llm]
```

**Core behavior (always, no flags needed):** Creates the `.cloche/` directory
structure if missing, creates or updates `.cloche/config.toml` (setting
`active = true`), adds `.gitignore` entries for runtime state, and registers
the project with the daemon.

| Flag | Default | Description |
|------|---------|-------------|
| `-n`, `--new` | false | Generate workflow files, Dockerfile, prompt templates, and scripts. First-project experience. Existing files are skipped. |
| `--install-shell-helpers` | false | Install shell completion scripts and add sourcing lines to `.bashrc` / `.zshrc`. One-time per-machine setup. |
| `--workflow <name>` | `develop` | Workflow name (only with `--new`). Creates `.cloche/<name>.cloche`. |
| `--base-image <base>` | `cloche-agent:latest` | Base Docker image for the generated Dockerfile (only with `--new`). |
| `--agent-command <cmd>` | _(see below)_ | LLM command for the init analysis phase (only with `--new`; overrides config and env). |
| `--no-llm` | false | Skip the LLM-assisted placeholder filling phase (only with `--new`). |

**`--new` scaffolding:** Creates `.cloche/` with workflow file, Dockerfile,
prompt templates (`implement.md`, `fix-tests.md`, `fix-merge.md`), host
workflows (`host.cloche`), Python scripts (`get-tasks.py`, `claim-task.py`,
`prepare-merge.py`, `merge.py`, `release-task.py`, `cleanup.py`, `unclaim.py`),
`.cloche/task_list.json`, and `cloche_init_test/cloche/test_cloche.py`. Skips
existing files.

Three generated files contain `TODO(cloche-init)` placeholders:

- **`.cloche/Dockerfile`** — dependency installation block with commented examples
  for Python, Node.js, Go, Java, and Ruby.
- **`.cloche/<name>.cloche`** — the `test` step `run` command.
- **`.cloche/prompts/implement.md`** — the `## Project Context` section describing
  your project's language, test command, and key conventions.

After scaffolding with `--new`, `cloche init` invokes the configured LLM client
to analyze the project (reading `go.mod`, `package.json`, `Makefile`, etc.) and
fill in these placeholders automatically. LLM command resolution order:

1. `--agent-command` flag
2. `CLOCHE_AGENT_COMMAND` environment variable
3. Global config `[daemon]` `llm_command`
4. `claude` if available on PATH

The LLM phase has a 30-second timeout and is non-fatal — if no LLM is available or
the phase fails, a warning is printed and the placeholders are left for manual
editing. Use `--no-llm` to skip the phase entirely (for CI or scripted setups).

Use `grep -r 'TODO(cloche-init)' .cloche/` to find any remaining placeholders.

Also creates `~/.config/cloche/config` (global daemon config) if it does not already
exist. The default config enables the web dashboard on `localhost:8080`. Skipped if
the file already exists.

### `cloche doctor`

Diagnose Cloche infrastructure.

```
cloche doctor [--project <dir>] [--verbose] [--timeout <duration>]
```

Runs checks in order and prints a status line for each. Exits with code 1
if any check fails. Checks 5–8 only run when the current (or `--project`) directory
contains a `.cloche/` subdirectory.

| Check | Description |
|-------|-------------|
| Docker | Runs `docker info` to verify the Docker daemon is reachable. |
| Base image | Checks whether `cloche-base:latest` (or `cloche-agent:latest`) exists locally. |
| Daemon | Calls `GetVersion` over gRPC to verify the daemon is reachable. Address from `CLOCHE_ADDR` or default `127.0.0.1:50051`. |
| Agent auth | Checks `ANTHROPIC_API_KEY` or `~/.claude/` session data. Soft check (warning, not fatal). |
| Project config | Loads `.cloche/config.toml`, reports parse errors, warns if `active = false` or `TODO(cloche-init)` markers remain. |
| Workflow syntax | Parses all `.cloche/*.cloche` files using the same logic as `cloche validate`. |
| Project image build | Calls `EnsureImage` to build or confirm the project Docker image. |
| Agent roundtrip | Starts a short-lived container from the project image, runs a minimal test workflow, and verifies it completes. |

Each failing check prints actionable remediation steps inline.

| Flag | Description |
|------|-------------|
| `--verbose`, `-v` | Print details for all checks (version, detected credential source, etc.). |
| `--project <dir>` | Run against the specified project directory instead of the current working directory. |
| `--timeout <duration>` | Timeout for the agent roundtrip check (default `60s`). The container is always cleaned up. |

### `cloche run`

Launch a workflow run.

```
cloche run <workflow>[:<step>] [--prompt "..."] [--title "..."] [--issue ID] [--keep-container]
```

| Argument / Flag | Description |
|-----------------|-------------|
| `<workflow>` | Workflow name. Resolves to `.cloche/<name>.cloche`. |
| `<workflow>:<step>` | Run starting at a specific step within the workflow. |
| `--prompt "..."`, `-p` | Inline prompt written to `.cloche/<run-id>/prompt.txt`. |
| `--title "..."` | One-line summary for status display. Auto-generated if omitted. |
| `--issue ID`, `-i` | Associate an existing task/issue ID with the run. Without this flag, a User-Initiated task is created automatically. |
| `--keep-container` | Keep container on success (failed runs always keep it). |

Must be run from inside a git repository. The daemon auto-rebuilds the Docker image
when `.cloche/Dockerfile` changes.

The command prints the workflow ID, task ID, and attempt ID on success. Use the task ID
with `cloche status`, `cloche logs`, and `cloche list`.

### `cloche resume`

Resume a failed workflow run from a specific step.

```
cloche resume <task-id>
cloche resume <workflow-id>
cloche resume <step-id>
```

| Argument | Description |
|----------|-------------|
| `<task-id>` | Bare task or run ID (no colons, e.g. `cloche-k4gh`). Resolves to the latest attempt's failed run and resumes from the first failed step. |
| `<workflow-id>` | Colon-separated workflow identifier. Accepted formats: `attempt:workflow` (e.g. `a133:develop`) or `task:attempt:workflow` (e.g. `TASK-123:a41k:develop`). Resumes from the first failed step. |
| `<step-id>` | Colon-separated step identifier: `attempt:workflow:step` (e.g. `a133:develop:review`). Resumes from that specific step. |

**Prerequisites:** The run must be in a failed state. For container workflows, the
container must still exist (failed runs keep their containers by default).

A step is considered failed — and therefore resumable — if it produced either an `error`
result (the step crashed) or a `fail` result via normal wiring (e.g. wired to `abort`).
When resolving the resume step automatically, the earliest such step is chosen.

**How resume works:** Resume creates a new Attempt (with a fresh attempt ID) rather
than modifying the failed run. The previous attempt and its run record remain in their
failed state for lineage tracing. The new attempt's `PreviousAttemptID` field points
back to the old attempt. The command returns the new workflow ID and new attempt ID.

- **Host workflows:** Successful step outputs from the previous attempt are copied into
  the new attempt's directory. The new run executes from the resume step forward, with
  those copied outputs available for wire output mappings.
- **Container workflows:** Each container from the failed attempt is committed to a
  Docker image (`docker commit`), capturing its filesystem state. New containers are
  started from those images (named `cloche-resume:<attemptID>-<containerID>`). The
  daemon engine re-walks the workflow graph: completed steps are skipped and their
  results pre-loaded from the database; remaining steps are dispatched to the new
  containers. Committed images are removed when the new attempt succeeds or the run is
  explicitly deleted; they are kept on failure so the user can resume again.

**Step-specific resume behavior:**

| Step type | Behavior |
|-----------|----------|
| script | Reruns the script fresh. Updated scripts are picked up. |
| prompt | Resumes the conversation instead of starting a new one. The agent receives a `resume=true` flag via the `ExecuteStep` message and continues the existing LLM conversation rather than starting fresh. |
| workflow | Same as script — starts the step again, passing values from previous steps' output. |

Steps that completed successfully before the resume point are skipped; their results
are replayed through the wiring so downstream steps receive the same inputs.

### `cloche status`

```
cloche status [<task-id>] [--all]
```

Without an ID, shows a daemon status overview. With a task ID, shows the latest attempt
status for that task.

| Argument | Output |
|----------|--------|
| Task ID | Task status, title, project, latest attempt ID, result, end timestamp, and total tokens consumed across all attempts (omitted if no usage data). When the task is `waiting` at a human step, also shows the step name, time since last poll, and poll count (e.g. `Waiting: code-review — last polled 4m ago (3 polls)`). |
| _(none)_ | Daemon version, run statistics (past hour), active tasks with attempt IDs and in-progress runs shown as composite IDs (e.g. `cloche-1234:aj19:main`), and per-agent token burn rate for the last hour (omitted if no usage data). In a project directory, also shows project name, concurrency, and loop state. |

| Flag | Description |
|------|-------------|
| `--all` | Show global stats instead of project-specific stats (overview mode only). |
| `--no-color` | Disable ANSI color output (also respects the `NO_COLOR` env var). Set `CLOCHE_FORCE_COLOR=1` to force color on even when stdout is not a terminal. |

### `cloche list`

```
cloche list [flags]
```

Lists tasks for the current project directory, grouped by status with attempt count and
latest attempt ID. Pass `--all` to show tasks across all projects. Use `--runs` to
show a flat run listing instead of the task-oriented view.

| Flag | Description |
|------|-------------|
| `--all` | Show tasks from all projects (default: current project only). |
| `--project, -p DIR` | Filter by project directory. |
| `--state, -s STATE` | Filter by task status (`pending`, `running`, `waiting`, `succeeded`, `failed`, `cancelled`). |
| `--limit, -n NUM` | Limit the number of results returned. |
| `--runs` | Show flat run listing instead of task-oriented view. |

Default output columns: task ID, status, attempt count, latest attempt ID, title.
With `--runs`: workflow ID, workflow, state, type, task ID, title, error.

### `cloche logs`

```
cloche logs <id> [--type <full|script|llm>] [-f] [-l <n>]
```

The first argument accepts any level of the ID hierarchy:

| Form | Example | Scope |
|------|---------|-------|
| Task ID | `shandalar-1234` | Logs for the latest attempt |
| Attempt ID | `a3f7` | Logs for that attempt |
| Workflow ID | `a3f7:develop` | Logs for that workflow run |
| Step ID | `a3f7:develop:implement` | Logs for that step |

Legacy composite `task:attempt[:step]` is also accepted.

| Flag | Description |
|------|-------------|
| `--type <full\|script\|llm>` | Log type filter. |
| `--follow, -f` | Follow mode: display existing logs then continue streaming new lines as they arrive (like `tail -f`). |
| `--limit, -l <n>` | Display only the last n lines of output. |

Flags are combinable: `cloche logs a3f7:develop:implement -l 20 -f`

Without `-f`, displays all logs captured to date and exits (even for active runs). With `-f` on an active run, existing logs are sent first, then new output is streamed in real time via gRPC until the run completes.

### `cloche poll`

```
cloche poll <id> [id...] [--no-color]
```

Block until all specified targets finish. Polls every 2 seconds. Exits 0 if all runs succeeded, 1 if any failed or were cancelled.

Accepts any level of the ID hierarchy:

| Form | Example | Behaviour |
|------|---------|-----------|
| Task ID | `shandalar-1234` | waits for the most recent run of that task |
| Attempt ID | `a133` | waits for that attempt |
| Workflow ID | `a133:develop` | waits for that specific workflow run |
| Step ID | `a133:develop:review` | waits until that step completes, then exits 0 |

Polling a step ID is useful for waiting on a long-running step without waiting for the whole run to finish.

| Flag | Description |
|------|-------------|
| `--no-color` | Disable ANSI color output (also respects the `NO_COLOR` env var). Set `CLOCHE_FORCE_COLOR=1` to force color on even when stdout is not a terminal. |

With a single ID, prints step-level progress. With multiple IDs, displays a compact status summary (e.g. `id1: running`) and re-prints whenever a state changes. Use `cloche logs` for detailed output of individual runs.

### `cloche stop`

```
cloche stop <task-id>
```

Stop all active runs for a task. Container runs have their container terminated; host runs (including user-initiated runs) have their execution cancelled. All affected run states transition to "cancelled".

### `cloche delete`

```
cloche delete <container-or-run-id>
```

Delete a retained Docker container by container ID or run ID.

### `cloche health`

```
cloche health
```

Show per-project pass/fail summary. Requires `CLOCHE_HTTP`.

### `cloche workflow`

```
cloche workflow [--project <dir>]
cloche workflow <name> [--project <dir>]
```

List all workflows or render a specific workflow as an ASCII-art graph.

| Flag | Default | Description |
|------|---------|-------------|
| `--project <dir>`, `-p` | current directory | Project directory to search for workflows. |

With no arguments, lists all workflows grouped by type (container or host). With a
workflow name, renders the workflow graph showing step boxes, wiring, and result paths.
Wires are colorized: green for `success`, red for `fail`/`failed`,
blue/yellow/orange/magenta for other results. Wires to the same destination are merged
for readability.

### `cloche validate`

Validate project configuration and workflow definitions.

```
cloche validate [--project <path>] [--workflow <name>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--project <path>` | current directory | Project directory to validate. |
| `--workflow <name>` | _(all)_ | Validate only the named workflow instead of all workflows. |

Checks performed:

- **config.toml** — parses correctly, fields are valid.
- **Workflow files** — syntax, result wiring completeness, terminal coverage (all paths reach `done`/`abort`), no orphan steps, config key validation.
- **File references** — prompt `file()` paths resolve to `.cloche/prompts/`, script `run` paths resolve to `.cloche/scripts/`.
- **Cross-file consistency** — `workflow_name` references in steps resolve to defined workflows.

Exits 0 and prints "All configuration valid." on success. Exits 1 and prints each error with file path on failure.

### `cloche project`

Show project info and config.

```
cloche project [--name <label>]
```

By default, looks up the project by the current working directory. Use `--name` to look
up a project by its registered label instead.

| Flag | Default | Description |
|------|---------|-------------|
| `--name <label>` | _(cwd lookup)_ | Look up project by label (e.g. `cloche`) instead of directory. |

Output includes: config settings (active, concurrency, stagger, dedup, stop_on_error,
max_consecutive_failures, evolution), orchestrator loop state (running/stopped/halted),
currently active runs, and known container and host workflow names. When the loop is
halted due to `stop_on_error`, `max_consecutive_failures`, or a container infrastructure
failure (image build failure, container crash, unexpected exit, or stuck workflow
detected), the halt error message is displayed.

### `cloche get`

```
cloche get <key>
```

Get a value from the daemon's gRPC-backed KV store. Requires the `CLOCHE_TASK_ID`
environment variable (`CLOCHE_ATTEMPT_ID` is also used if set). Exits 1 if the key is
not found.

### `cloche set`

```
cloche set <key> <value|->
```

Set a value in the daemon's gRPC-backed KV store. Requires the `CLOCHE_TASK_ID`
environment variable (`CLOCHE_ATTEMPT_ID` is also used if set).
Pass `-` as the value to read from stdin (trailing newlines are trimmed).
Pass `-f <file>` to read the value from a file.

#### Auto-Seeded Keys

The following keys are written automatically and can be read with `cloche get`:

**Run-level** (set once at run start):

| Key | Value |
|-----|-------|
| `task_id` | Task identifier |
| `attempt_id` | Attempt identifier |
| `workflow` | Current workflow name |
| `run_id` | Run identifier |

**Step-level** (updated before each step):

| Key | Value |
|-----|-------|
| `prev_step` | Name of the step that triggered this one (empty for the entry step) |
| `prev_step_exit` | Result of that step (empty for the entry step) |

**Step result tracking** (set after each step completes):

| Key | Value |
|-----|-------|
| `<workflow>:<step>:result` | Result code of the completed step (e.g. `develop:implement:result = success`) |

**Sub-workflow results** (set after a `workflow_name` step targeting a container workflow completes):

| Key | Value |
|-----|-------|
| `child_run_id` | Run ID of the child container workflow; used to locate the `cloche/<run-id>` git branch with extracted results |

All auto-seeded keys are fully writable — scripts can overwrite them with `cloche set`.

### `clo` (In-Container KV CLI)

`clo` is a lightweight binary available inside containers for reading and writing the
daemon's KV store without needing the full `cloche` client.

```
clo get <key>              Print value to stdout; exit 1 if not found
clo set <key> <value>      Set a key
clo set <key> -            Read value from stdin (trailing newlines trimmed)
clo set <key> -f <file>    Set a key from file contents
clo keys                   List all keys in the current attempt namespace
clo -v / --version         Print version
```

`clo` reads `CLOCHE_ADDR`, `CLOCHE_TASK_ID`, and `CLOCHE_ATTEMPT_ID` from the
environment. The Docker adapter sets all three automatically.

### `cloche tasks`

```
cloche tasks [--project <dir>]
```

Show the task pipeline and assignment state for an orchestration loop. Displays
upcoming/open tasks, which tasks are assigned to which runs, and auto-assignment
state. Requires `CLOCHE_HTTP` (talks to the daemon's web API).

| Flag | Default | Description |
|------|---------|-------------|
| `--project <dir>` | current directory name | Project to query tasks for. |

### `cloche loop`

Start or stop the daemon's orchestration loop. The loop automatically picks up
tasks from the pipeline and runs them.

```
cloche loop [--max <n>]
cloche loop once
cloche loop stop
```

| Flag | Default | Description |
|------|---------|-------------|
| `--max <n>` | config value | Maximum concurrent runs. Defaults to the value in `.cloche/config.toml`. |

`cloche loop once` starts the loop, waits for a single task to be picked up and
completed, then automatically stops the loop. Exits 0 on success, 1 on failure or
cancellation.

`cloche loop stop` disables the loop. Running tasks are not cancelled.

When `stop_on_error` or `max_consecutive_failures` triggers a stop, run `cloche loop`
again to restart the loop.

### `cloche activity`

Show the project activity log — attempt and step lifecycle events with timestamps and outcomes.

```
cloche activity [--project <dir>] [--since <duration|time>] [--until <time>] [--json]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--project <dir>`, `-p` | current directory | Project directory to read the log from. |
| `--since <value>` | _(all)_ | Show only entries on or after this time. Accepts a Go duration (`24h`, `7d`, `30m`) or an RFC3339 timestamp. |
| `--until <time>` | _(all)_ | Show only entries on or before this RFC3339 timestamp. |
| `--json` | false | Output raw JSONL instead of the table view. |

Reads activity entries from the daemon's SQLite database. Events are recorded automatically by the orchestration loop and host workflow runs. Output columns: `TIME`, `KIND`, `TASK`, `ATTEMPT`, `WORKFLOW`, `STEP`, `OUTCOME`.

Event kinds: `attempt_started`, `attempt_ended`, `step_started`, `step_completed`.

```
cloche activity
cloche activity --since 24h
cloche activity --since 7d
cloche activity --since 2026-03-01T00:00:00Z
cloche activity --project /path/to/project
cloche activity --json
```

### `cloche --version`

Print version information for all Cloche components.

```
cloche -v
cloche --version
```

Displays the CLI version, queries the running daemon for its version (via gRPC),
and runs `docker run --rm --entrypoint cloche-agent <image> -v` to get the agent
version from the project's container image. If the daemon is unreachable or the
image doesn't exist, the corresponding version shows `<unavailable>`. Version
mismatches between components produce warnings on stderr.

The daemon and agent binaries also support standalone version output:

```
cloched -v       # prints daemon version
cloche-agent -v  # prints agent version
```

### `cloche shutdown`

Sends a shutdown signal to the daemon. Refuses to shut down if there are active runs
unless `--force` is specified.

```
cloche shutdown [--force|-f] [--restart|-r]
```

Flags:
- `-f`, `--force` — Shut down even if runs are still active.
- `-r`, `--restart` — Relaunch the daemon after stopping it (or start it if it is not
  already running). The new daemon process is detached so the CLI can exit immediately.

### `cloche console`

Starts an interactive agent session in a fresh container using the project's image and
setup (project files, auth credentials, overrides) — same environment as a workflow run,
without running a workflow.

```
cloche console [--agent <command>]
```

Flags:
- `--agent <command>` — Override the agent command to run inside the container. Defaults
  to the same resolution chain as workflow runs: workflow config → `CLOCHE_AGENT_COMMAND`
  env var → `claude`.

The terminal is put into raw mode and I/O is forwarded bidirectionally through the daemon.
Terminal resize events (SIGWINCH) are forwarded automatically. When the session ends, the
container is kept — it does not appear in `cloche list`, but can be deleted with
`cloche delete <container-id>` or `docker rm <id>`. The container ID is printed on exit.

Must be run from inside a git repository with a `.cloche/` directory.

### `cloche complete`

Low-level helper used by shell completion scripts. Not intended for direct use.

```
cloche complete --index <n> -- <word0> <word1> ...
```

Prints one completion candidate per line for the word at position `<n>` in the
command line. If the daemon is reachable, dynamic candidates (task IDs, workflow
names, attempt IDs) are returned via the `Complete` gRPC RPC. Otherwise falls
back to static subcommand and flag completions.

**Context-aware filtering:** The daemon filters candidates based on the subcommand:
- `status` and `poll` — only suggest tasks that are currently running or completed
  within the last ~10 minutes.
- `stop` — only suggests currently running tasks.
- `logs`, `resume`, `delete` — suggest recent runs (last 20).

**Fuzzy matching:** Candidates are matched against the typed prefix using
colon-delimited component matching. For example, typing a partial attempt ID
like `1fka` will expand to the full composite ID `task-abc:1fka`, so you don't
need to know the task ID prefix to tab-complete an attempt.

The shell integration is set up by running `cloche init --install-shell-helpers`, which writes
`~/.cloche/completions/cloche.bash` (for bash) and `~/.cloche/completions/_cloche`
(for zsh) and offers to append the sourcing snippet to `~/.bashrc` or `~/.zshrc`.
This is a one-time per-machine setup and is not required for each project.

## Project Layout

```
my-project/
├── .cloche/
│   ├── develop.cloche        # Container workflow
│   ├── host.cloche           # Host orchestration workflows (contain host { } blocks)
│   ├── Dockerfile            # Container image definition
│   ├── config.toml           # Project configuration
│   ├── version               # Schema version marker (used for upgrade checks)
│   ├── prompts/              # Prompt templates
│   │   ├── implement.md
│   │   ├── fix-tests.md
│   │   └── fix-merge.md
│   ├── scripts/              # Host-side scripts
│   ├── overrides/            # Files copied on top of /workspace/
│   │   └── CLAUDE.md         # Container-specific CLAUDE.md (optional)
│   ├── task_list.json        # Sample task file for local development and testing
│   ├── runs/
│   │   └── <task-id>/        # Runtime state (gitignored)
│   │       └── prompt.txt    # User prompt
│   ├── activity.log          # Append-only JSONL activity log (attempt/step events)
│   └── logs/
│       └── <task-id>/        # Grouped by task (ticket or user-initiated run)
│           └── <attempt-id>/ # One directory per attempt
│               ├── full.log                  # Unified log (all steps)
│               ├── <workflow>-<step>.log     # Per-step script output
│               └── <workflow>-llm-<step>.log # Per-step LLM conversation
├── cloche_init_test/
│   └── cloche/
│       └── test_cloche.py    # Validation tests for the Cloche setup
├── .clocheignore             # Workspace file exclusions (patterns to omit from container)
├── src/                      # Project source (untouched by Cloche)
└── .git/
```

## Project Configuration Reference

`.cloche/config.toml` is the per-project configuration file (created by `cloche init`).

| Key | Default | Description |
|-----|---------|-------------|
| `active` | `true` | Set to `true` to auto-start the orchestration loop when the daemon starts. |

### `[orchestration]`

| Key | Default | Description |
|-----|---------|-------------|
| `concurrency` | `1` | Maximum concurrent container runs. |
| `stagger_seconds` | `1.0` | Delay (seconds) between consecutive run launches. |
| `dedup_seconds` | `300` | Window (seconds) to suppress re-assigning the same task ID. |
| `stop_on_error` | `false` | Halt the orchestration loop on the first unrecovered error. |
| `max_consecutive_failures` | `3` | Stop the loop after this many consecutive failed runs. Run `cloche loop` to restart. |

### `[evolution]`

Controls the self-evolving prompt system. Requires `CLOCHE_LLM_COMMAND` to be set;
evolution is silently disabled when it is not.

| Key | Default | Description |
|-----|---------|-------------|
| `enabled` | `true` | Enable or disable evolution for this project. |
| `debounce_seconds` | `30` | Seconds to wait after a run completes before triggering an evolution pass (debounces rapid successive completions). |
| `min_confidence` | `"medium"` | Minimum lesson confidence to include in prompts. One of `"low"`, `"medium"`, `"high"`. |

### `[agents.codex]`

Per-agent config for the Codex agent. See [How to set up Codex](agent-setup-codex.md) for full details.

| Key | Default | Description |
|-----|---------|-------------|
| `usage_command` | _(unset)_ | Shell command run after each Codex step to capture token usage. Output must be JSON: `{"input_tokens": N, "output_tokens": N}`. |

## Environment Variable Reference

### Daemon Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_ADDR` | `127.0.0.1:50051` | gRPC listen address |
| `CLOCHE_DB` | `~/.config/cloche/cloche.db` | SQLite database path |
| `CLOCHE_RUNTIME` | `docker` | `docker` or `local` (subprocess, for dev only) |
| `CLOCHE_IMAGE` | `cloche-agent:latest` | Default Docker image |
| `CLOCHE_HTTP` | `localhost:8080` (via global config) | HTTP address for web dashboard. Not started unless set. |
| `CLOCHE_AGENT_PATH` | _(auto)_ | Path to `cloche-agent` binary (local runtime) |
| `CLOCHE_LLM_COMMAND` | _(unset)_ | Command for LLM calls (evolution, merge conflicts) |
| `ANTHROPIC_API_KEY` | _(unset)_ | Passed into Docker containers |
| `CLOCHE_EXTRA_MOUNTS` | _(unset)_ | Extra bind mounts (comma-separated `host:container`) |
| `CLOCHE_EXTRA_ENV` | _(unset)_ | Extra env vars (comma-separated `KEY=VALUE`) |

### Client Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `CLOCHE_ADDR` | `127.0.0.1:50051` | Daemon gRPC address |
| `CLOCHE_HTTP` | `localhost:8080` | Daemon HTTP address |

### Host Step Runtime Variables

Set by the daemon for each host step script invocation.

| Variable | Description |
|----------|-------------|
| `CLOCHE_PROJECT_DIR` | Absolute path to the project directory on the host. |
| `CLOCHE_RUN_ID` | The run ID for this workflow execution. |
| `CLOCHE_STEP_OUTPUT` | Path where this step should write its output (for output mappings). |
| `CLOCHE_PREV_OUTPUT` | Path to the output file from the immediately preceding step. |
| `CLOCHE_TASK_ID` | Task ID assigned by the daemon (set for the `main` phase). |

### Container Runtime Variables

Injected into the container by the daemon at startup.

| Variable | Description |
|----------|-------------|
| `CLOCHE_RUN_ID` | The run ID for this workflow execution. |
| `CLOCHE_TASK_ID` | Task ID assigned by the daemon. Set when the container run is associated with a task. |
| `CLOCHE_ATTEMPT_ID` | Attempt identifier for this container run. Used for unique container naming. |
| `CLOCHE_PROJECT_DIR` | Working directory inside the container (`/workspace`). Set so `cloche get`/`cloche set` work correctly. |
| `CLOCHE_AGENT_COMMAND` | Overrides the default agent command inside the container. |
| `CLOCHE_ADDR` | Daemon gRPC TCP address (e.g. `host.docker.internal:50051`). Used by `clo get`/`clo set` inside the container. |
| `ANTHROPIC_API_KEY` | Passed through from the host environment if set. |

## Dockerfile Requirements

The container image must have:
- `cloche-agent` binary at `/usr/local/bin/cloche-agent`
- `clo` binary at `/usr/local/bin/clo`
- `git` installed
- An `agent` user (cloche wraps commands with `chown` + `su agent`)
- `/workspace` as the working directory
- Your project's build dependencies and the agent binary (e.g. `claude`)

## Build Commands

```
make build          # Build all binaries to bin/
make test           # Run all tests
make test-short     # Run tests (skip slow ones)
make lint           # Run go vet
make proto          # Regenerate gRPC code from protobuf
make docker-build   # Build the cloche-agent Docker image
make install        # Build, install to ~/.local/bin/, restart daemon
make clean          # Remove bin/
```
