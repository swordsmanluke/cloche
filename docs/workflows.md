# Workflows

Cloche workflows define a directed graph of steps connected by named results.

## Concepts

| Concept  | Description                                                        |
|----------|--------------------------------------------------------------------|
| workflow | A named graph of steps connected by result wiring                  |
| step     | A unit of work (inferred: `prompt` = agent, `run` = script)        |
| result   | A named outcome reported by a step (e.g. "success", "fail")       |
| wiring   | Maps a step's result to the next step                              |
| done     | Built-in terminal step — successful completion                     |
| abort    | Built-in terminal step — failure with error reporting              |

## Step Types

Step type is inferred from the fields present in the step body:

**agent** (has `prompt`) — Invokes a coding agent (Claude Code, or any tool conforming
to the agent adapter interface) with a prompt. The agent works autonomously inside the
container.

**script** (has `run`) — Runs a shell command. Used for tests, linters, validators, or
any deterministic check.

A step with both `prompt` and `run`, or neither, is a parse error.

## DSL Syntax

```
workflow "implement-feature" {
  step code {
    prompt = file("prompts/implement.md")
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
    prompt = file("prompts/review.md")
    input = step.code.output
    results = [approved, changes_requested]
  }

  // Wiring: step:result -> next_step
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

## Execution Model

The workflow graph is parsed and executed by `cloche-agent` inside the container.
The agent walks the graph: execute current step, read its result, follow the wiring to
the next step. This continues until a terminal step (`done` or `abort`) is reached.

All steps in a workflow run inside the same container. File state accumulates naturally
across steps.
