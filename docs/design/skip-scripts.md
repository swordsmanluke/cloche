# Design: Skip Scripts

## Overview

A **skip script** is an optional shell command attached to a workflow step. When
present, it runs before the step's real work and decides whether to skip the step
entirely. The exit code is the decision; stdout optionally names the wire to follow
when skipping.

The use case is a fast pre-check that lets a workflow short-circuit work that has
already been done: e.g. "is this task already implemented on the current branch?",
"is the PR already open?", "does the cached artifact still match this commit?". A
non-zero exit means the check could not establish that the step is unnecessary, and
the step runs as usual.

## DSL

A single new step config key, `skip`. It accepts any shell command (interpreted via
`sh -c`, the same as `run` and `poll`):

```
step develop {
  prompt  = file(".cloche/prompts/develop.md")
  skip    = "scripts/already-implemented.sh"
  results = [success, fail, needs-research]
}
```

Allowed on any step type (`agent`, `script`, `workflow`, `poll`, `human`). The key
is added to `knownStepConfigKeys` in `internal/domain/workflow.go`.

## Semantics

| Skip outcome                            | Behaviour                                                                          |
|-----------------------------------------|------------------------------------------------------------------------------------|
| Exit 0, no marker on stdout             | Skip the step. Follow the `success` wire. Runtime error if the step has no `success` result. |
| Exit 0, `CLOCHE_RESULT:<wire>` emitted  | Skip the step. Follow the named wire. Runtime error if `<wire>` is not declared in the step's `results`. |
| Exit non-zero                           | Run the step normally.                                                              |
| Timeout (90s), signal, crash            | Treated as non-zero. Run the step normally.                                         |

The wire-name marker reuses `protocol.ExtractResult`, the same parser the script
and poll step types already use. Marker lines are stripped from captured output.

The skip script has a hardcoded **90s timeout**. It is intentionally not configurable
in the first cut — skip checks are expected to be fast probes. 90s is chosen to be
slightly over the typical 60s network timeout so a single hung network call doesn't
deterministically blow up the gate.

If the skip script itself fails (timeout, signal, non-zero exit, anything), the step
runs. This is the safe default: a broken gate must never silently swallow work.

## Execution location

Skip scripts run **in the same location as the step itself**:

- **Host workflow steps** → daemon runs the skip script on the host. Working
  directory is the main git worktree, identical to host-workflow script and poll
  steps. The same `CLOCHE_*` environment variables (`CLOCHE_PROJECT_DIR`,
  `CLOCHE_RUN_ID`, `CLOCHE_TASK_ID`, `CLOCHE_ATTEMPT_ID`) are injected.
- **Container workflow steps** → `cloche-agent` runs the skip script inside the
  container, in `/workspace/`, with the container's environment. This means the
  container is started and ready before the skip script runs; we do not get the
  "skip avoids container startup" win, but we get consistency, identical filesystem
  semantics, and `clo get` / `clo set` access to the run's KV store.

## Lifecycle integration

**`max_attempts`.** A skipped invocation does not consume an attempt. Today the
engine increments `stepLaunchCounts[stepName]` in `launchStep`
(`internal/engine/engine.go`); this must move to after the skip decision so it only
fires when the step actually executes.

**Resume.** Skip scripts do **not** re-run on resume. A previously skipped step is
recorded with its chosen wire and is replayed via `preloadedResults` like any other
completed step.

**Poll steps.** Skip is evaluated once when the step launches, before the polling
loop starts. It is "should we engage at all", not "should we poll this tick".

**Workflow steps.** Skipping a `workflow_name` step means the sub-workflow is never
dispatched. No run row is created for the inner workflow.

## Logs and status

Captured skip output is written to `step.<name>.skip.log` alongside the step's
existing output capture. The `CLOCHE_RESULT:` marker line is stripped before
writing.

A new step status `skipped` (distinct from `completed` and `error`) is recorded on
the run and surfaced in `cloche status` / `cloche list`. The recorded result is the
wire the skip chose.

A new event is emitted on the status handler:

```go
type StatusHandler interface {
    OnStepStart(run *domain.Run, step *domain.Step)
    OnStepComplete(run *domain.Run, step *domain.Step, result string, usage *domain.TokenUsage)
    OnStepSkipped(run *domain.Run, step *domain.Step, wire string) // new
    OnRunComplete(run *domain.Run)
}
```

A dedicated hook keeps UI/CLI rendering simple: adapters can label skipped steps
distinctly without having to inspect a flag on `OnStepComplete`.

## Protocol

The `ExecuteStep` gRPC message gains a `skip_command` string field. When non-empty,
the agent runs it before dispatching to the underlying adapter (prompt, script,
poll). The agent reports the decision via a new `StepSkipped { wire }` message
when the skip takes effect, or proceeds with normal execution and sends `StepResult`
as today otherwise.

## Validation

- Parse-time: `skip` is recognised as a step config key. No additional structural
  validation — the wire name returned by the script can only be checked at runtime
  against the step's declared `results`.
- Runtime: an undeclared wire from a skip script is reported the same as an
  undeclared result from a regular step.

## Implementation surface

- `internal/domain/workflow.go` — register `skip` in `knownStepConfigKeys`; add
  `StepStatusSkipped` constant.
- `internal/engine/engine.go` — wrap the launch path: run skip first, move attempt
  counter, emit `OnStepSkipped`, record skipped status on the run.
- `internal/engine/context.go` — extend `StatusHandler` with `OnStepSkipped`; add
  default no-op to existing adapters.
- `internal/host/executor.go` — host-side skip runner. Mirrors the existing
  `sh -c` env-injection path used for script steps.
- `internal/protocol/...` — add `skip_command` to `ExecuteStep` and `StepSkipped`
  message to the agent stream.
- `internal/agent/session.go` and `internal/adapters/agents/...` — in-container
  skip runner that runs before the adapter dispatch.
- `internal/run/...` — surface the new `skipped` status in run records and in
  `cloche status` / `cloche list`.
- `docs/workflows.md` — add a "Skip scripts" subsection.

## Non-goals

- No configurable skip timeout in the first cut. Hardcoded at 90s.
- No retry / backoff for failing skip scripts. A failure runs the step.
- No way for a skip script to abort the run directly. To abort, it must emit a
  wire that the user has explicitly routed to `abort`.
- No host-side fast-path for container steps. Containers are started before the
  skip runs. Revisit only if a real workload makes this measurable.
