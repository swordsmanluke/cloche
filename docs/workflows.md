# Workflows

Cloche workflows define a directed graph of steps connected by named results.

## Workflow Locations

Cloche distinguishes two workflow locations based on the file they live in:

**Container workflows** (`.cloche/*.cloche` except `host.cloche`) — Run inside a Docker
container via `cloche-agent`. Steps may only be `agent` or `script` type. These are the
standard workflows for coding tasks.

**Host workflows** (`.cloche/host.cloche`) — Run on the host machine as the daemon
process. Steps may be `agent`, `script`, or `workflow` type. The `workflow` step type
dispatches a container workflow run and waits for it to complete. This is the extension
point for custom orchestration strategies.

A single `host.cloche` file may contain **multiple named workflows**. The daemon
uses up to three workflow phases for orchestration:

| Phase | Workflow name | Purpose |
|-------|--------------|---------|
| 1 | `list-tasks` | Discover available work. Output is JSONL (one task per line). |
| 2 | `main` | Do the work. Receives a task ID via `CLOCHE_TASK_ID` env var. |
| 3 | `finalize` | Post-main cleanup. Runs on **both** success and failure. |

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
until it completes. Only available in host workflows (`host.cloche`).

A step with more than one of `prompt`, `run`, or `workflow_name`, or none of them, is a
parse error.

## DSL Syntax

```
workflow "implement-feature" {
  step code {
    prompt = file(".cloche/prompts/implement.md")
    container {
      image = "cloche/agent:latest"
      network_allow = ["docs.python.org"]
    }
    results = [success, fail, retry_with_feedback]
  }

  step check {
    run = "make test && make lint"
    results = [pass, fail]
  }

  step review {
    prompt = file(".cloche/prompts/review.md")
    input = step.code.output
    results = [approved, changes_requested]
  }

  // Wiring: step:result -> next_step [optional output mappings]
  code:success -> check
  code:fail -> abort
  code:retry_with_feedback -> code

  check:pass -> review
  check:fail -> code:retry_with_feedback

  review:approved -> done
  review:changes_requested -> code
}
```

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

## The `host {}` Block

Host workflows support a `host {}` block at the workflow level to configure agent
defaults for agent steps running on the host machine. Keys are stored with a `host.`
prefix, analogous to the `container {}` block for container workflows.

```
workflow "main" {
  host {
    agent_command = "claude"
  }
  ...
}
```

Supported keys: `agent_command`, `agent_args`. Step-level `agent_command` and
`agent_args` override the workflow-level `host {}` defaults. The agent command
resolution order for host agent steps is the same as for container agent steps:
step-level > workflow-level `host {}` > `CLOCHE_AGENT_COMMAND` env var > default
`claude`.

## Host Workflow Example

A three-phase `host.cloche` with separate `list-tasks`, `main`, and `finalize`
workflows:

```
# .cloche/host.cloche — runs on the host machine

workflow "list-tasks" {
  step fetch-tickets {
    run     = "bash .cloche/scripts/ready-tasks.sh"
    results = [success, fail]
  }

  fetch-tickets:success -> done
  fetch-tickets:fail    -> abort
}

workflow "main" {
  host {
    agent_command = "claude"
  }

  step prepare-prompt {
    run     = "bash .cloche/scripts/prepare-prompt.sh"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  prepare-prompt:success -> develop
  prepare-prompt:fail    -> abort
  develop:success        -> done
  develop:fail           -> done
}

workflow "finalize" {
  step push-for-review {
    run     = "bash .cloche/scripts/push-for-review.sh"
    results = [success, fail]
  }

  push-for-review:success -> done
  push-for-review:fail    -> done
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

**Host workflows** are parsed and executed by the daemon on the host machine. A single
`host.cloche` file may contain multiple named workflows; the daemon orchestrates them
in three phases (list-tasks → main → finalize). Script steps run via `sh -c` with the
working directory set to the **main git worktree** (i.e. the main branch checkout), even
if the project directory is a linked worktree on a different branch. This ensures
host-workflow scripts from main are used for all runs. Workflow steps dispatch container
runs via the daemon's standard run pipeline. Environment variables (`CLOCHE_TASK_ID`,
`CLOCHE_PROJECT_DIR`, etc.) are injected into each step; `CLOCHE_PROJECT_DIR` still
points to the actual project directory.
