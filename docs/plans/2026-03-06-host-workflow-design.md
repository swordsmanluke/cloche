# Host Workflow Design (`host.cloche`)

**Date:** 2026-03-06
**Status:** Design

## Problem

The orchestrator's intake → process → output loop is hard-coded in Go
(`internal/orchestrator/orchestrator.go`). Every project must follow the same flow:
pull task → generate prompt → dispatch `develop` container run. This can't be changed
without modifying Cloche itself. Projects that need different orchestration strategies
(push to GitHub for review, group tickets into feature branches, stacked commits, etc.)
have no extension point.

## Solution: `host.cloche`

A special workflow file at `.cloche/host.cloche`. It uses the same DSL as container
workflow files, with one additional step type (`workflow`). Steps in this file execute
on the **host machine**, not inside a container.

The workflow named `main` in this file becomes the per-task orchestration loop
for that project. The Go daemon becomes the executor of this workflow rather than the
hard-coded logic holder.

### File location

```
.cloche/host.cloche
```

If the file does not exist, the daemon falls back to the current hard-coded behavior
(pull task → LLM prompt generation → dispatch container workflow).

---

## DSL additions

### New step type: `workflow`

A step with `workflow_name` config key is a workflow-dispatch step. It dispatches a
named container workflow run and blocks until that run completes.

```
step develop {
  workflow_name = "develop"
  results = [success, fail]
}
```

`workflow_name` is added to `knownStepConfigKeys` in `domain/workflow.go`.

`StepTypeWorkflow` is added to the `StepType` enum in `domain/workflow.go`.

The parser (`dsl/parser.go`) infers `StepTypeWorkflow` when `workflow_name` is present
(same pattern as `run` → script, `prompt` → agent).

### Prompt input for `workflow` steps

The container workflow needs a prompt. By convention, a `workflow` step reads the
previous step's captured output file as the prompt. This is the primary data-passing
mechanism.

An optional config key `prompt_step` overrides which step's output to read:
```
step develop {
  workflow_name = "develop"
  prompt_step   = "prepare-prompt"
  results       = [success, fail]
}
```

---

## Step output capture

Every step in a host workflow execution writes its output (stdout of `run` steps,
final response of `prompt` steps) to a file:

```
.cloche/<orch-run-id>/main/<step_name>.out
```

`<orch-run-id>` is a unique ID for this orchestration invocation, formatted the same
way as workflow run IDs (e.g. `main-bold-hawk`).

The daemon tracks the most recently completed step's output path and passes it to the
next step as `CLOCHE_PREV_OUTPUT`.

---

## Environment variables injected into host steps

All `run` and `prompt` steps receive:

| Variable | Value |
|----------|-------|
| `CLOCHE_TASK_ID` | Task ID being processed |
| `CLOCHE_TASK_TITLE` | Task summary/title |
| `CLOCHE_TASK_BODY` | Task full body/description |
| `CLOCHE_PROJECT_DIR` | Absolute path to the project directory |
| `CLOCHE_STEP_OUTPUT` | Path where this step should write its output |
| `CLOCHE_PREV_OUTPUT` | Path to the previous step's output file |

For `workflow` steps, `CLOCHE_PREV_OUTPUT` is read automatically (unless `prompt_step`
overrides it).

---

## Executor: `internal/orchestrator/hostrunner.go`

New file. Implements the host workflow execution loop.

```go
type HostRunner struct {
    Dispatch  RunDispatcher      // existing: dispatches a container workflow run
    WaitRun   RunWaiter          // new: blocks until a run ID completes, returns final state
    AgentCmd  string             // command to invoke for prompt steps on host
}

// RunWorkflow executes the named workflow from a parsed host.cloche Workflow
// for a single task. Returns the final result ("done" or "abort").
func (r *HostRunner) RunWorkflow(ctx context.Context, wf *domain.Workflow, task ports.Task, orchRunID string) (string, error)
```

Step execution:

- **`script` step** (`run` key): `exec.Command` with env vars, stdout captured to
  `<step>.out`, stderr to `<step>.err`. Exit 0 → `success`, non-zero → `fail`.
- **`agent` step** (`prompt` key): invoke `AgentCmd` on host with the prompt file
  contents injected. Capture output. Exit 0 → `success`, non-zero → `fail`.
- **`workflow` step** (`workflow_name` key): read prompt from prev step's `.out` file
  (or `prompt_step` override), call `Dispatch`, then call `WaitRun` to block until
  the container run completes. Map `succeeded` → `success`, `failed`/`cancelled` →
  `fail`.

`WaitRun` is a new port interface:

```go
// RunWaiter blocks until the run with the given ID reaches a terminal state.
type RunWaiter interface {
    WaitRun(ctx context.Context, runID string) (domain.RunState, error)
}
```

The gRPC server / store already has `GetRun`; the waiter can poll it with backoff or
subscribe to the `OnRunComplete` event. A simple polling implementation (1s interval)
is sufficient initially.

---

## Refactored `orchestrator.Orchestrator`

`Orchestrator.Run()` is updated:

**Current flow (hardcoded):**
1. Pull ready tasks
2. Claim task
3. Call `promptGen.Generate(task)`
4. Call `dispatch(workflow, projectDir, prompt)` → runID
5. (In-flight counter managed, `OnRunComplete` decrements it)

**New flow (when `host.cloche` exists):**
1. Pull ready tasks
2. Claim task
3. Parse `.cloche/host.cloche` → find `main` workflow
4. Generate an `orchRunID` (`main-<namegen>`)
5. Spin up a goroutine: call `hostRunner.RunWorkflow(ctx, orch wf, task, orchRunID)`
6. On goroutine return: decrement in-flight, update task state (success/fail)

The old `promptGen` and `dispatch` are still used as the **fallback** when
`host.cloche` is absent (no breaking change for existing projects).

`WaitRun` needs access to the run store. Wire it in `initOrchestrator` in
`cmd/cloched/main.go`.

---

## Default `host.cloche` (from `cloche init`)

```
# host.cloche — orchestration workflow (runs on host, not in container)
# Steps here execute as the daemon user. Keep operations simple and safe.

workflow "main" {
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
```

And `.cloche/scripts/prepare-prompt.sh` (created by init, chmod +x):

```bash
#!/usr/bin/env bash
# Default prompt generator. Uses task env vars injected by cloched.
# Write the prompt to $CLOCHE_STEP_OUTPUT (also echoed to stdout).
set -euo pipefail

prompt="## Task: ${CLOCHE_TASK_TITLE}

${CLOCHE_TASK_BODY}"

echo "$prompt"
[ -n "${CLOCHE_STEP_OUTPUT:-}" ] && echo "$prompt" > "$CLOCHE_STEP_OUTPUT"
```

---

## `.gitignore` update

`cloche init` adds `.cloche/scripts/` is NOT gitignored (scripts are project config).
The orchestration run output dirs follow the same pattern as container run dirs:
`.cloche/main-*/` is gitignored via the existing `.cloche/*/` rule.

---

## Safety notes

- Host steps run with the same OS privileges as the `cloched` process.
- No network isolation.
- `host.cloche` should only reference scripts that are committed to the project.
- Document that `prompt` steps in `host.cloche` invoke an LLM agent with full host
  filesystem access — use judiciously.

---

## Changes required

| File | Change |
|------|--------|
| `internal/domain/workflow.go` | Add `StepTypeWorkflow`, add `workflow_name`/`prompt_step` to `knownStepConfigKeys` |
| `internal/dsl/parser.go` | Infer `StepTypeWorkflow` when `workflow_name` key is present |
| `internal/orchestrator/hostrunner.go` | New: host workflow executor |
| `internal/orchestrator/orchestrator.go` | Refactor `Run()` to use `HostRunner` when `host.cloche` exists; fall back to current behavior otherwise |
| `internal/ports/container.go` | Add `RunWaiter` interface |
| `cmd/cloched/main.go` | Wire `HostRunner` into orchestrator init |
| `cmd/cloche/init.go` | Create `host.cloche` and `scripts/prepare-prompt.sh` |
