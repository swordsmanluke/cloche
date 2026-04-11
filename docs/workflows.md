# Workflows

Cloche workflows define a directed graph of steps connected by named results.

## Workflow Locations

Cloche distinguishes two workflow locations based on the `host { }` block:

**Container workflows** — The default. Run inside a Docker container via `cloche-agent`.
Steps may be `agent`, `script`, or `workflow` type. These are the standard workflows for
coding tasks.

**Host workflows** — Declared by including a `host { }` block in the workflow definition.
Run on the host machine as the daemon process. Steps may be `agent`, `script`, `workflow`,
or `human` type. This is the extension point for custom orchestration strategies.

Any `.cloche` file can contain host workflows — they are not restricted to a specific
filename. A single file may contain **multiple named workflows**. The daemon uses up to
two workflow phases for orchestration:

| Phase | Workflow name | Purpose |
|-------|--------------|---------|
| 1 | `list-tasks` | Discover available work. Output is JSONL (one task per line). |
| 2 | `main` | Do the work. Receives a task ID via `CLOCHE_TASK_ID` env var. |

Additionally, a **`release-task`** host workflow may be defined. This is not part of the
automatic orchestration loop — it is invoked on demand (e.g. from the web dashboard) to
release a stale claimed task back to `open` status. Receives `CLOCHE_TASK_ID` for the
task to release.

Only `main` is required. If `list-tasks` is absent, the daemon runs `main`
continuously using a sentinel task (no task ID tracking).

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
compatibility) and runs `main` with that task ID. Tasks are deduplicated within a
configurable timeout window to prevent rapid reassignment.

Workflow names are **project-unique identifiers**. The daemon scans all `.cloche` files
via `FindAllWorkflows()` and returns an error if the same workflow name appears in more
than one file. `FindHostWorkflows()` is a filtered view of this result, returning only
workflows with a `host {}` block.

## Concepts

| Concept  | Description                                                        |
|----------|--------------------------------------------------------------------|
| workflow | A named graph of steps connected by result wiring                  |
| step     | A unit of work (`agent`, `script`, `workflow`, or `human` type)    |
| result   | A named outcome reported by a step (e.g. "success", "fail")       |
| wiring   | Maps a step's result to the next step, with optional output mappings |
| agent    | A named agent declaration with command and arguments               |
| done     | Built-in terminal step — successful completion                     |
| abort    | Built-in terminal step — failure with error reporting              |

## Step Types

Step type is inferred from the fields present in the step body, except for `human` which
must be declared explicitly with `type = human`:

**agent** (has `prompt`) — Invokes a coding agent (Claude Code, or any tool conforming
to the agent adapter interface) with a prompt. The agent works autonomously inside the
container. Available in both host and container workflows. Default timeout: 30m.

**script** (has `run`) — Runs a shell command. Used for tests, linters, validators, or
any deterministic check. Available in both host and container workflows. Default timeout: 30m.

**workflow** (has `workflow_name`) — Triggers a named workflow run and blocks until it
completes. Available in both host and container workflows. Dispatch is always handled by
the daemon orchestrator, which resolves the target workflow by name across all `.cloche`
files and routes it to the host executor or container pool as appropriate. Default timeout: 30m.

**human** (`type = human`) — Polls a script at a fixed interval until a human decision
is available. Requires `script` (path to the polling script) and `interval` (poll
frequency, e.g. `"5m"`). The script exit code and stdout determine the outcome: exit 0
with no wire output means pending (poll again); non-zero exit with no wire output follows
the `fail` wire; any exit with wire output follows the named wire. Default timeout: 72h.
Host workflows only. See [Human Step](#human-step) below.

All step types support a `timeout` config key (any `time.ParseDuration` value, e.g.
`"45m"`, `"2h"`). When a step exceeds its timeout, it produces a `"timeout"` result. If
no `timeout` wire is declared, the implicit wire routes to `abort`.

A step with more than one of `prompt`, `run`, or `workflow_name`, or none of them, is a
parse error. A `human` step must not include `prompt`, `run`, or `workflow_name`.

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
field makes it a script step. `type = human` is explicit and cannot be inferred.

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

## Parallel Branches (Fanout)

Wire one result to multiple targets for concurrent execution:

```
test:success -> lint
test:success -> quality
```

## Collect (Join)

Synchronize parallel branches:

```
collect all(lint:success, quality:success) -> done
collect any(lint:success, quality:success) -> done
```

`all` fires when every condition is met. `any` fires when at least one is.

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

Supported keys: `id`, `image`, `agent_command`, `agent_args`, `network_allow`, `memory`.

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

## Container IDs

Every container workflow has a **container id** that identifies which shared container it
uses for a given run attempt. The id is set via the `id` key in the `container {}` block.
If no `id` is declared, the workflow uses the implicit default id `_default`. All
container workflows sharing the same id run inside the same container per attempt.

```
workflow "develop" {
  container {
    id    = "dev-env"
    image = "my-project:latest"
  }
  ...
}

workflow "review" {
  container {
    id = "dev-env"   // shares the same container as "develop"
  }
  ...
}
```

### Cross-Workflow Validation

`cloche validate` enforces that all workflows sharing a container id have consistent
configuration. Exactly one of the following must hold for any group of workflows with
the same id:

**(a) All have full config and it is identical** — every workflow in the group provides
the same set of container config keys with the same values.

**(b) One has full config, others declare only `id`** — exactly one workflow provides
the image and agent settings; the rest reference the id with no other config.

**(c) All declare only `id`** — none of the workflows provide any container config
beyond the id field itself.

Any other combination (e.g. two workflows sharing an id with different `image` values)
is a validation error.

## Host Workflow Example

A two-phase host workflow setup with `list-tasks` and `main` (this is the default
scaffold generated by `cloche init`):

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

The daemon runs `list-tasks` to discover work (output is JSONL), picks an open task,
and runs `main` with `CLOCHE_TASK_ID` set. The `list-tasks` script writes one JSON
object per line to `$CLOCHE_STEP_OUTPUT`. Post-run cleanup steps (merging, closing
tickets, etc.) belong directly in `main`.

## Execution Model

**Container workflows** are executed by the daemon, which walks the workflow graph and
dispatches each step to the `cloche-agent` running inside a Docker container. The daemon
sends `ExecuteStep` commands over a bidirectional `AgentSession` gRPC stream; the agent
executes the step and sends back a `StepResult`. The daemon then follows the wiring to
the next step. All steps in the same container ID share one container per attempt.
File state accumulates naturally across steps.

**Host workflows** (those with a `host { }` block) are parsed and executed by the daemon
on the host machine. Any `.cloche` file may contain host workflows; the daemon
orchestrates them in two phases (list-tasks → main). Script steps run via
`sh -c` with the
working directory set to the **main git worktree** (i.e. the main branch checkout), even
if the project directory is a linked worktree on a different branch. This ensures
host-workflow scripts from main are used for all runs. Workflow steps (`workflow_name`)
dispatch container runs through the daemon. Environment variables (`CLOCHE_TASK_ID`,
`CLOCHE_PROJECT_DIR`, etc.) are injected into each step; `CLOCHE_PROJECT_DIR` still
points to the actual project directory.

## Human Step

A `human` step polls a script at a fixed interval until a human decision is returned,
the step times out, or the script fails. It is declared with an explicit `type = human`
field rather than inferred from content.

**Required fields:** `type = human`, `script`, `interval`

```
step "code-review" {
  type     = human
  script   = "scripts/check-pr-review.sh"
  interval = "5m"
  timeout  = "48h"   // optional; default is 72h
}

code-review:approved -> merge
code-review:fix      -> address-feedback
code-review:timeout  -> escalate
```

**Poll script exit semantics:**

| Exit code | Wire output | Meaning |
|-----------|-------------|---------|
| 0         | none        | Pending — poll again after `interval` |
| non-zero  | none        | Failure — follow the `fail` wire |
| any       | wire name   | Decision — follow the named wire |

The first poll runs immediately when the step starts; subsequent polls fire after each
`interval`. If a poll invocation is still running when the next tick fires, that tick is
skipped. After 3 consecutive skips (poll running for more than 4× the interval) the step
fails with an error message.

The default timeout for `human` steps is **72h** (versus 30m for agent/script/workflow
steps). If no `timeout` wire is declared, timeout follows the implicit `abort` terminal.
