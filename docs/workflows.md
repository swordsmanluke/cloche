# Workflows

Cloche workflows define a directed graph of steps connected by named results.

## Workflow Locations

Cloche distinguishes two workflow locations based on the `host { }` block:

**Container workflows** — The default. Run inside a Docker container via `cloche-agent`.
Steps may only be `agent` or `script` type. These are the standard workflows for coding
tasks.

**Host workflows** — Declared by including a `host { }` block in the workflow definition.
Run on the host machine as the daemon process. Steps may be `agent`, `script`, or
`workflow` type. The `workflow` step type dispatches a container workflow run and waits
for it to complete. This is the extension point for custom orchestration strategies.

Any `.cloche` file can contain host workflows — they are not restricted to a specific
filename. A single file may contain **multiple named workflows**. The daemon uses up to
three workflow phases for orchestration:

| Phase | Workflow name | Purpose |
|-------|--------------|---------|
| 1 | `list-tasks` | Discover available work. Output is JSONL (one task per line). |
| 2 | `main` | Do the work. Receives a task ID via `CLOCHE_TASK_ID` env var. |
| 3 | `finalize` | Post-main cleanup. Runs on **both** success and failure. |

Additionally, a **`release-task`** host workflow may be defined. This is not part of the
automatic orchestration loop — it is invoked on demand (e.g. from the web dashboard) to
release a stale claimed task back to `open` status. Receives `CLOCHE_TASK_ID` for the
task to release.

Only `main` is required. If `list-tasks` is absent, the daemon uses a legacy
single-function mode. If `finalize` is absent, it is skipped.

The `list-tasks` workflow's final step output is parsed as JSONL. Each line is a JSON
object with the following fields:

| Field | Required | Description |
|-------|----------|-------------|
| `id` | yes | Unique task identifier |
| `status` | yes | One of `open`, `closed`, `in-progress` |
| `title` | no | Short summary |
| `description` | no | Full description |
| `metadata` | no | Arbitrary key-value pairs |

The daemon picks the first task with status `open` (or empty, for backward
compatibility), runs `main` with that task ID, then runs `finalize` with the outcome.
Tasks are deduplicated within a configurable timeout window to prevent rapid
reassignment.

The convention is enforced at parse time: a `workflow_name` step in a container workflow
file is a parse error.

## Concepts

| Concept  | Description                                                        |
|----------|--------------------------------------------------------------------|
| workflow | A named graph of steps connected by result wiring                  |
| step     | A unit of work (inferred: `prompt` = agent, `run` = script)        |
| result   | A named outcome reported by a step (e.g. "success", "fail")       |
| wiring   | Maps a step's result to the next step, with optional output mappings |
| agent    | A named agent declaration with command and arguments               |
| done     | Built-in terminal step — successful completion                     |
| abort    | Built-in terminal step — failure with error reporting              |

## Step Types

Step type is inferred from the fields present in the step body:

**agent** (has `prompt`) — Invokes a coding agent (Claude Code, or any tool conforming
to the agent adapter interface) with a prompt. The agent works autonomously inside the
container. Available in both host and container workflows.

**script** (has `run`) — Runs a shell command. Used for tests, linters, validators, or
any deterministic check. Available in both host and container workflows.

**workflow** (has `workflow_name`) — Dispatches a named container workflow run and blocks
until it completes. Only available in host workflows (those with a `host { }` block).

A step with more than one of `prompt`, `run`, or `workflow_name`, or none of them, is a
parse error.

## DSL Syntax

```
workflow "develop" {
  container {
    image = "my-project:latest"
    agent_command = "claude"
  }

  step implement {
    prompt = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step test {
    run = "make test && make lint"
    results = [success, fail]
  }

  step fix {
    prompt = file(".cloche/prompts/fix.md")
    max_attempts = 3
    results = [success, fail, give-up]
  }

  // Wiring: step:result -> next_step [optional output mappings]
  implement:success -> test
  implement:fail    -> abort
  test:success      -> done
  test:fail         -> fix
  fix:success       -> test
  fix:fail          -> abort
  fix:give-up       -> abort
}
```

## Agent Declarations

Workflows can declare named agents at the workflow level. An agent specifies a `command`
(required) and optional `args`. Steps reference agents by identifier via the `agent`
config key, avoiding repetition of agent configuration across multiple prompt steps.

```
workflow "develop" {
  agent claude {
    command = "claude"
    args = "-p --output-format stream-json"
  }

  agent codex {
    command = "codex"
    args = "--full-auto"
  }

  step implement {
    prompt = file(".cloche/prompts/implement.md")
    agent = claude
    results = [success, fail]
  }

  step review {
    prompt = file(".cloche/prompts/review.md")
    agent = codex
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
| `command` | yes | The agent command to run (e.g. `claude`, `codex`, `ollama`) |
| `args` | no | Arguments passed to the agent command |

**Resolution:** When a step references an agent via `agent = <identifier>`, the agent's
`command` and `args` are expanded into `agent_command` and `agent_args` on the step.
Step-level `agent_command` and `agent_args` still override the agent declaration. The
full resolution order is: step-level > agent declaration > workflow-level block >
`CLOCHE_AGENT_COMMAND` env var > default `claude`.

**Validation:** Referencing an undeclared agent is a validation error. Only prompt (agent
type) steps may reference an agent. Duplicate agent names within a workflow are a parse
error. An agent declaration without a `command` field is a parse error.

## Key Properties

**Step type is inferred from content.** A `prompt` field makes it an agent step; a `run`
field makes it a script step.

**Steps declare their possible results.** A step decides at runtime which result to
report. The graph engine follows the wiring to determine the next step.

**Wiring is separate from step definitions.** This enables inserting new steps between
existing ones by rewiring — without modifying either step. This is the foundation for
Cloche's self-evolution feature.

**Graphs are validated at parse time.** The parser checks that all declared results are
wired, no steps are orphaned, and an entry point exists.

## Wire Output Mappings

Wires can include output mappings that extract values from a step's output and inject
them as environment variables into the target step:

```
step-a:success -> step-b [ ENV_VAR = output.field, OTHER = output.list[0].name ]
```

The general form:

```
FROM:RESULT -> TO [ KEY = EXPR, KEY = EXPR, ... ]
```

Where `KEY` is the environment variable name and `EXPR` is an output path expression
starting with the contextual keyword `output`:

| Expression | Meaning |
|---|---|
| `output` | Raw output (full string) |
| `output.key` | JSON object field access |
| `output[N]` | JSON array index (0-based) |
| `output.a.b.c` | Deeply nested field access |
| `output.items[0].name` | Mixed field and index chaining |

If the source step's output is valid JSON, path expressions navigate the parsed
structure. If the output is plain text, only bare `output` is valid. The resolved
value is converted to a string for env var injection.

If two wires targeting the same step both map the same env var key, validation
returns an error (the mapping would be ambiguous). The same key may be used on
wires to different steps without conflict.

## Workflow-Level Configuration Blocks

Workflows support a configuration block at the workflow level to set defaults for all
steps. The block name depends on the workflow location:

**`container {}`** — For container workflows. Sets container image, agent command, and
network allowlist.

```
workflow "develop" {
  container {
    image = "my-project:latest"
    agent_command = "claude"
    agent_args = "-p --dangerously-skip-permissions"
  }
  ...
}
```

Supported keys: `image`, `agent_command`, `agent_args`, `network_allow`.

**`host {}`** — Declares a workflow as a host workflow. Can appear in any `.cloche` file.
Sets agent defaults for agent steps running on the host machine. An empty `host {}` block
(no keys) is valid and simply marks the workflow as host-side.

```
workflow "main" {
  host {
    agent_command = "claude"
  }
  ...
}
```

Supported keys: `agent_command`, `agent_args`.

Step-level `agent_command` and `agent_args` override workflow-level defaults. The
resolution order is: step-level > agent declaration > workflow-level block >
`CLOCHE_AGENT_COMMAND` env var > default `claude`.

## Host Workflow Example

A three-phase host workflow setup with separate `list-tasks`, `main`, and `finalize`
workflows (this is the default scaffold generated by `cloche init`):

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

  claim-task:success -> develop
  claim-task:fail    -> abort
  develop:success    -> done
  develop:fail       -> done
}

workflow "finalize" {
  host {}

  step prepare-merge {
    run     = "python3 .cloche/scripts/prepare-merge.py"
    results = [success, fail]
  }

  step fix-merge {
    prompt  = file(".cloche/prompts/fix-merge.md")
    results = [success, fail]
  }

  step merge {
    run     = "python3 .cloche/scripts/merge.py"
    results = [success, fail]
  }

  step release-task {
    run     = "python3 .cloche/scripts/release-task.py"
    results = [success, fail]
  }

  step cleanup {
    run     = "python3 .cloche/scripts/cleanup.py"
    results = [success, fail]
  }

  step unclaim {
    run     = "python3 .cloche/scripts/unclaim.py"
    results = [success, fail]
  }

  prepare-merge:success -> merge
  prepare-merge:fail    -> fix-merge
  fix-merge:success     -> merge
  fix-merge:fail        -> unclaim
  merge:success         -> release-task
  merge:fail            -> fix-merge
  release-task:success  -> cleanup
  release-task:fail     -> unclaim
  cleanup:success       -> done
  cleanup:fail          -> unclaim
  unclaim:success       -> abort
  unclaim:fail          -> abort
}
```

The daemon runs `list-tasks` to discover work (output is JSONL), picks an open task,
runs `main` with `CLOCHE_TASK_ID` set, then runs `finalize` regardless of outcome.
The `list-tasks` script writes one JSON object per line to `$CLOCHE_STEP_OUTPUT`.
The `main` workflow receives the task ID and is responsible for claiming the ticket.
The `finalize` workflow decides what to do based on the outcome (available via
`CLOCHE_MAIN_OUTCOME` and `CLOCHE_MAIN_RUN_ID` env vars).

## Execution Model

**Container workflows** are parsed and executed by `cloche-agent` inside a Docker
container. The agent walks the graph: execute current step, read its result, follow the
wiring to the next step. This continues until a terminal (`done` or `abort`) is reached.
All steps run inside the same container. File state accumulates naturally across steps.

**Host workflows** (those with a `host { }` block) are parsed and executed by the daemon
on the host machine. Any `.cloche` file may contain host workflows; the daemon
orchestrates them in three phases (list-tasks → main → finalize). Script steps run via
`sh -c` with the
working directory set to the **main git worktree** (i.e. the main branch checkout), even
if the project directory is a linked worktree on a different branch. This ensures
host-workflow scripts from main are used for all runs. Workflow steps dispatch container
runs via the daemon's standard run pipeline. Environment variables (`CLOCHE_TASK_ID`,
`CLOCHE_PROJECT_DIR`, etc.) are injected into each step; `CLOCHE_PROJECT_DIR` still
points to the actual project directory.
