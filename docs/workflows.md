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

Step type is inferred from the fields present in the step body:

**agent** (has `prompt`) — Invokes a coding agent (Claude Code, or any tool conforming
to the agent adapter interface) with a prompt. The agent works autonomously inside the
container. Available in both host and container workflows. Default timeout: 30m.

**script** (has `run`) — Runs a shell command. Used for tests, linters, validators, or
any deterministic check. Available in both host and container workflows. Default timeout: 30m.

**workflow** (has `workflow_name`) — Triggers a named workflow run and blocks until it
completes. Available in both host and container workflows. Dispatch is always handled by
the daemon orchestrator, which resolves the target workflow by name across all `.cloche`
files and routes it to the host executor or container pool as appropriate. Default timeout: 30m.

**poll** (has `poll`) — Polls a script at a fixed interval until a decision is available.
Requires `poll` (the polling script/command) and `interval` (poll frequency, e.g. `"5m"`).
The script exit code and stdout determine the outcome: exit 0 with no wire output means
pending (poll again); non-zero exit with no wire output follows the `fail` wire; any exit
with wire output follows the named wire. Default timeout: 72h. Host workflows only. See
[Poll Step](#poll-step) below.

All step types support a `timeout` config key (any `time.ParseDuration` value, e.g.
`"45m"`, `"2h"`). When a step exceeds its timeout, it produces a `"timeout"` result. If
no `timeout` wire is declared, the implicit wire routes to `abort`.

A step with more than one of `prompt`, `run`, `workflow_name`, or `poll`, or none of them,
is a parse error.

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
  agent haiku_claude {
    command = "claude"
    args = "-p --dangerously-skip-permissions --model claude-haiku-4-5"
  }

  agent codex {
    command = "codex"
    args = "--full-auto"
  }

  step implement {
    prompt = file(".cloche/prompts/implement.md")
    agent = haiku_claude
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
field makes it a script step; a `poll` field makes it a poll step.

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

  claim-task:success    -> develop
  claim-task:fail       -> abort
  develop:success       -> prepare-merge
  develop:fail          -> unclaim
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

A `human` step pauses a workflow until an external decision is available. Instead of
blocking a long-running process, it polls a user-supplied script at a fixed interval
until the script reports a decision, the step times out, or the script fails. Typical
use cases: waiting for a code review, an approval gate, a CI run, or a ticket
transition.

Host workflows only. Poll steps are not executed inside the container `cloche-agent`
— they must live in a workflow with a `host { }` block. Attempting to use `poll` in a
container workflow results in the step being unhandled at runtime.

### Declaration

Like other step types, a poll step is inferred from its fields: the presence of `poll`
makes it a poll step. A poll step must not include `prompt`, `run`, or `workflow_name`
— those are parse errors (a step can have exactly one type keyword).

| Field      | Required | Description |
|------------|----------|-------------|
| `poll`     | yes      | Shell command to invoke on each poll. Run via `sh -c`, so pipes, redirection, and shell builtins all work. Can be a path to a script (`scripts/check.sh`), an inline command (`gh pr view 123 --json state -q .state`), or anything sh accepts. |
| `interval` | yes      | Poll frequency as a Go duration — any value accepted by [`time.ParseDuration`](https://pkg.go.dev/time#ParseDuration), e.g. `"30s"`, `"5m"`, `"1h"`. Invalid durations fail at parse time. |
| `timeout`  | no       | Overall step timeout. Default `72h` (vs. `30m` for agent/script/workflow steps). When the step exceeds its timeout it produces a `"timeout"` result. |
| `results`  | no       | Declared wire names. Always implicitly includes `timeout` if omitted. Your declared list must cover every `CLOCHE_RESULT:<name>` value the script can emit, plus `fail` if your script can exit non-zero without a marker. |

```
step code-review {
  poll     = "scripts/check-pr-review.sh"
  interval = "5m"
  timeout  = "48h"
  results  = [approved, fix]
}

code-review:approved -> merge
code-review:fix      -> address-feedback
code-review:timeout  -> escalate     // or omit; default routes to abort
```

### Reporting results

The polling script reports a decision exactly like a script step: print
`CLOCHE_RESULT:<wire-name>` on its own line. Markers are read from the script's
**combined** stdout+stderr; the last marker line wins; marker lines are stripped from
the captured output before it is written to the step output file.

**Exit semantics:**

| Exit code | `CLOCHE_RESULT` marker | Outcome |
|-----------|------------------------|---------|
| 0         | none                   | **Pending** — try again after `interval`. |
| 0         | `<name>`               | **Decision** — follow the named wire. |
| non-zero  | none                   | **Failure** — follow the `fail` wire. |
| non-zero  | `<name>`               | **Decision** — follow the named wire. The non-zero exit is ignored when a marker is present. |

Exit 0 with no marker is what makes a `human` step a `human` step: it's the "not yet"
signal that tells the orchestrator to keep polling.

### Polling cadence

The first poll fires immediately when the step starts; subsequent polls fire no sooner
than `interval` after the previous poll completes. The orchestration loop drives polls
centrally — the executor goroutine is parked on a result channel and does not consume a
process or thread between polls.

`interval` is a **no-sooner-than** constraint, not a strict schedule. Actual poll times
depend on the loop tick rate (and on how long the previous invocation took), so expect
polls to land within ~30s of the ideal time.

**Overlapping invocations.** If a poll is still running when the next interval comes
due, that interval is skipped. If a single invocation runs longer than 4× `interval`
(three consecutive skips), the step produces a `fail` result and follows the `fail`
wire.

### Timeout

The default step timeout is 72 hours. Override it with the `timeout` field, same as any
other step type. When the timeout expires the step produces a `"timeout"` result.

A `timeout` wire and an implicit `timeout` result are added automatically if you
don't declare them explicitly — the implicit wire routes to `abort`. Declaring an
explicit `code-review:timeout -> <step>` wire suppresses the implicit `abort` wire.

### Script execution environment

Poll scripts run on the host (not in a container), under `sh -c`, with the working
directory set to the **main git worktree** of the project (same rule as host-workflow
script steps). The following environment variables are injected on every invocation:

| Variable              | Description |
|-----------------------|-------------|
| `CLOCHE_PROJECT_DIR`  | Absolute path to the project directory on the host. |
| `CLOCHE_STEP_OUTPUT`  | Path where this step's captured output file is written (same file is overwritten on every poll). |
| `CLOCHE_RUN_ID`       | Run ID for the current workflow run. |
| `CLOCHE_TASK_ID`      | Task ID being processed (if the workflow was launched from a task). |
| `CLOCHE_ATTEMPT_ID`   | Attempt ID for the current run attempt. |

The script inherits the rest of the daemon's environment, minus any existing
`CLOCHE_RUN_ID` from the daemon process itself.

### Reading run context

Poll scripts can read values written to the run's KV store by earlier steps via
`cloche get <key>` (host-side). Container steps in the same run write with
`clo set <key> <value>`. The KV store is persisted for the lifetime of the run.

```bash
# In an earlier container step (e.g. create-pr):
clo set pr_id 1234

# In the polling script:
pr_id=$(cloche get pr_id)
state=$(gh pr view "$pr_id" --json state -q .state)
case "$state" in
  MERGED) echo "CLOCHE_RESULT:approved" ;;
  CLOSED) echo "CLOCHE_RESULT:rejected" ;;
  *)      ;;  # still open → exit 0 with no marker → pending
esac
```

### Idempotency

Poll scripts are invoked **once per interval for the lifetime of the step** — which can
easily be dozens or hundreds of invocations over a 72-hour window. Any side effect
(posting a comment, sending a notification, creating a ticket) must be guarded so it
runs at most once. The typical pattern is to use the KV store to record that the side
effect has occurred:

```bash
if [ "$(cloche get notified_reviewer)" != "yes" ]; then
  slack-notify "@reviewer PR ready"
  cloche set notified_reviewer yes
fi
```

### Visibility in `cloche list` / `cloche status`

While a `human` step is active, its run's state is set to `waiting`, which `cloche list`
and `cloche status` surface distinctly from `running`. The daemon also records
`last_poll_at` and the step name in the `HumanPollStore` so you can see how long the
step has been waiting and when it last polled.

### Complete example

```
workflow "review-and-merge" {
  host {}

  step create-pr {
    run     = "python3 .cloche/scripts/create-pr.py"
    results = [success, fail]
  }

  step code-review {
    poll     = "python3 .cloche/scripts/check-review.py"
    interval = "5m"
    timeout  = "48h"
    results  = [approved, changes-requested]
  }

  step address-feedback {
    workflow_name = "fix-review-feedback"
    results       = [success, fail]
  }

  step merge {
    run     = "python3 .cloche/scripts/merge-pr.py"
    results = [success, fail]
  }

  step escalate {
    run     = "python3 .cloche/scripts/notify-oncall.py"
    results = [success]
  }

  create-pr:success             -> code-review
  create-pr:fail                -> abort
  code-review:approved          -> merge
  code-review:changes-requested -> address-feedback
  code-review:timeout           -> escalate
  address-feedback:success      -> code-review      // re-poll after pushing fixes
  address-feedback:fail         -> abort
  merge:success                 -> done
  merge:fail                    -> abort
  escalate:success              -> abort
}
```

## Built-in KV Keys

The daemon automatically sets the following KV keys before the first step of every run.
They are available to host-side scripts via `cloche get <key>` and to container steps
via `clo get <key>`.

| Key | Value | Description |
|-----|-------|-------------|
| `temp_file_dir` | `.cloche/runs/<run-id>` | Scratch directory for temp files too large for the 1 KB KV value limit. The daemon creates this directory at run start. Because it lives inside `.cloche/runs/`, it is already covered by the standard gitignore pattern. |
| `task_prompt_path` | `.cloche/runs/<run-id>/task_prompt.md` | Set by `prepare-prompt.sh` (or similar host scripts) to point at the task description file. Not set automatically by the daemon. |

Use `temp_file_dir` for intermediate files that need to be passed between steps:

```bash
# host script — write a large file
output="$(cloche get temp_file_dir)/review-feedback.md"
generate-feedback > "$output"
cloche set review_feedback_path "$output"

# container step — read it back
feedback_path=$(clo get review_feedback_path)
cat "$feedback_path"
```
