# Workflows

Cloche workflows define a directed graph of steps connected by named results.

## Workflow Locations

Cloche distinguishes two workflow locations based on the file they live in:

**Container workflows** (`.cloche/*.cloche` except `host.cloche`) — Run inside a Docker
container via `cloche-agent`. Steps may only be `agent` or `script` type. These are the
standard workflows for coding tasks.

**Host workflow** (`.cloche/host.cloche`) — Runs on the host machine as the daemon
process. Steps may be `agent`, `script`, or `workflow` type. The `workflow` step type
dispatches a container workflow run and waits for it to complete. This is the extension
point for custom orchestration strategies.

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

## Host Workflow Example

```
# .cloche/host.cloche — runs on the host machine
workflow "main" {
  step prepare-prompt {
    run     = "bash .cloche/scripts/prepare-prompt.sh"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    prompt_step   = "prepare-prompt"
    results       = [success, fail]
  }

  prepare-prompt:success -> develop
  prepare-prompt:fail    -> abort
  develop:success        -> done
  develop:fail           -> done
}
```

The `workflow_name` step reads the prompt from the previous step's output (or
`prompt_step` override), dispatches a container workflow run, and blocks until it
completes.

## Execution Model

**Container workflows** are parsed and executed by `cloche-agent` inside a Docker
container. The agent walks the graph: execute current step, read its result, follow the
wiring to the next step. This continues until a terminal (`done` or `abort`) is reached.
All steps run inside the same container. File state accumulates naturally across steps.

**Host workflows** are parsed and executed by the daemon on the host machine. Script
steps run via `sh -c`, and workflow steps dispatch container runs via the daemon's
standard run pipeline. Environment variables (`CLOCHE_TASK_ID`, `CLOCHE_PROJECT_DIR`,
etc.) are injected into each step.
