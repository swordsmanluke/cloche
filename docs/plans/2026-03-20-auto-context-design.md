# Auto-Seeded Run Context Design

**Date:** 2026-03-20
**Status:** Partially implemented (engine/context.go and engine.go done; runcontext seeding pending)

## Problem

Steps frequently need task ID, attempt ID, workflow name, previous step name, and
previous step result. Today these are either unavailable (attempt ID, workflow name)
or scattered across env vars (`CLOCHE_TASK_ID`, `CLOCHE_RUN_ID`). Scripts that need
this information resort to ad-hoc wiring or can't access it at all. There is no
standard way to discover what happened earlier in the run.

## Solution

Automatically seed well-known keys in the `cloche get/set` context file
(`context.json`) at run start and before/after each step. Steps use `cloche get`
to read any value they need — no env var hunting, no manual wiring. Over time,
env vars like `CLOCHE_TASK_ID` and `CLOCHE_RUN_ID` become redundant; the context
file is the single source of truth.

## Design Details

### Auto-Seeded Keys

**Run-level keys** — set once when the run starts:

| Key | Value | Example |
|-----|-------|---------|
| `task_id` | Task identifier | `shandalar-pn8r` |
| `attempt_id` | Attempt identifier | `jifo` |
| `workflow` | Current workflow name | `develop` |
| `run_id` | Run identifier | `jifo-develop` |

**Step-level keys** — updated before each step starts:

| Key | Value | Example |
|-----|-------|---------|
| `prev_step` | Name of the step that triggered this one | `implement` |
| `prev_result` | Result of that step (the wire that fired) | `success` |

For the entry step, `prev_step` and `prev_result` are set to empty string.

**Step result tracking** — set after each step completes:

| Key | Value | Example |
|-----|-------|---------|
| `<workflow>:<step>:result` | Result code of the completed step | `success`, `fail`, `give-up` |

Examples: `develop:implement:result = success`, `main:commit:result = fail`.
Since context is scoped to a single attempt, `workflow:step` is sufficient
to differentiate steps across workflows (e.g. `main:commit` vs `develop:commit`)
without needing the full run ID.

### Where Seeding Happens

Seeding happens in two places — the host executor and the container agent's
step executor — since these are the two execution environments.

**New function in `runcontext/store.go`:**

```go
// SeedRunContext writes the run-level auto-context keys.
// Called once at run start by both host and container executors.
func SeedRunContext(projectDir, taskID, attemptID, workflow, runID string) error

// SetPrevStep updates prev_step and prev_result before a step executes.
func SetPrevStep(projectDir, taskID, prevStep, prevResult string) error

// SetStepResult records a completed step's result as <workflow>:<step>:result.
func SetStepResult(projectDir, taskID, workflow, step, result string) error
```

These are thin wrappers around `Set()` — no new storage mechanism.

**Host executor** (`internal/host/executor.go`):

The `Executor` already has `TaskID`, `AttemptID`, and the workflow name is
available from the caller. The daemon seeds run-level context when it creates
the executor. Step-level context updates happen in `Execute()` before
dispatching to `executeScript`/`executeWorkflow`/`executeAgent`, and after
each returns with a result.

The engine doesn't call `Execute` with previous-step info today. To pass
`prev_step`/`prev_result` to the executor, add them to `context.Context`
using a package-level key in `engine/context.go`:

```go
// engine/context.go
type contextKey int

const stepTriggerKey contextKey = iota

type StepTrigger struct {
    PrevStep   string
    PrevResult string
}

func WithStepTrigger(ctx context.Context, t StepTrigger) context.Context
func GetStepTrigger(ctx context.Context) (StepTrigger, bool)
```

The engine's `launchStep` function accepts a `StepTrigger` parameter and
attaches it to the step's context before calling `e.executor.Execute(ctx, step)`.
For the entry step, the trigger has empty strings.

**Container agent** (`internal/agent/runner.go`):

The `Runner` seeds run-level context in `Run()` before calling `eng.Run()`.
The `stepExecutor.Execute()` reads the trigger from context (same mechanism)
and calls `runcontext.SetPrevStep` before executing. After the step returns,
it calls `runcontext.SetStepResult`.

The container needs `AttemptID` to seed it. Today only `CLOCHE_TASK_ID` and
`CLOCHE_RUN_ID` are passed. Add `CLOCHE_ATTEMPT_ID` to the Docker runtime
(`internal/adapters/docker/runtime.go`) alongside the existing env vars.

### Engine Changes (`internal/engine/engine.go`)

The engine's event loop already knows the previous step and result when it
resolves wiring. In `Run()`, after processing `sr` (step result) and before
calling `launchStep(target)`:

```go
// In the wiring resolution loop:
if err := launchStep(target, StepTrigger{PrevStep: sr.stepName, PrevResult: sr.result}); err != nil { ... }
```

`launchStep` gains a `trigger StepTrigger` parameter. Inside the goroutine it
calls `WithStepTrigger(stepCtx, t)` to attach the trigger before passing the
context to `Execute`. For the initial entry step launch, use a zero-value
`StepTrigger{}` (empty strings).

The engine also calls a new hook on `StatusHandler` after each step:

```go
type StatusHandler interface {
    OnStepStart(run *domain.Run, step *domain.Step)
    OnStepComplete(run *domain.Run, step *domain.Step, result string, usage *domain.TokenUsage)
    OnRunComplete(run *domain.Run)
}
```

No changes needed to `StatusHandler` — the executors handle context seeding
themselves using the trigger info from context.

### Container Runtime Changes

**`internal/adapters/docker/runtime.go`** — pass `CLOCHE_ATTEMPT_ID`:

```go
if cfg.AttemptID != "" {
    args = append(args, "-e", "CLOCHE_ATTEMPT_ID="+cfg.AttemptID)
}
```

**`cmd/cloche-agent/main.go`** — read and unset:

```go
attemptID := os.Getenv("CLOCHE_ATTEMPT_ID")
os.Unsetenv("CLOCHE_ATTEMPT_ID")
```

Pass through to `RunnerConfig` (add `AttemptID string` field).

### Writability

Auto-seeded keys are fully writable. Scripts can `cloche set prev_step foo`
if they want. No protection, no reserved-key enforcement. KISS.

### Parallel Step Execution

When a step fans out (e.g. `step-a:success -> step-b, step-c`), both `step-b`
and `step-c` get `prev_step=step-a`, `prev_result=success`. Since they write
to the same `context.json`, the last `SetPrevStep` call wins — but each step
reads its own trigger from `context.Context`, not from the file. The file
reflects the most recently launched step, which is fine for linear workflows
and acceptable for fan-out (scripts that need precise trigger info can check
`<workflow>:<step>:result` keys instead).

### Migration Path

Existing env vars (`CLOCHE_TASK_ID`, `CLOCHE_RUN_ID`, `CLOCHE_PROJECT_DIR`)
continue to work. No breaking changes. Over time, scripts migrate to
`cloche get task_id` etc. Env vars remain as a bootstrap mechanism — the
agent needs `CLOCHE_TASK_ID` to know which context file to read.

### Error Handling

Context seeding failures are logged but do not abort the run. The context
file is a convenience layer — if it can't be written (permissions, disk full),
steps still execute and can fall back to env vars. `runcontext.SeedRunContext`
and friends return errors; callers log them via `log.Printf` and continue.

## Alternatives Considered

**Expand env vars instead of context file.** Rejected because env vars can't
be updated mid-run (prev_step changes per step), can't store per-step result
history, and add clutter to the container environment. The context file is
already the mechanism for inter-step communication.

**Make auto-seeded keys read-only.** Rejected for simplicity. Adding
reserved-key checks complicates `Set()` and provides minimal benefit — if a
script overwrites `task_id`, that's the script's problem.

**Pass trigger info via the `StepExecutor` interface.** Rejected in favor of
`context.Context` because it avoids a breaking interface change and is more
idiomatic Go. Executors that don't need trigger info can ignore it.
