## Task: Fix: Timeout context cancellation must follow timeout wire, not fail the step

When a step's context deadline is exceeded, the executor currently returns `ctx.Err()` as an error. The engine treats this as a fatal execution failure rather than routing through the `timeout` wire. This breaks the universal timeout wire design for all step types.

## Bug

In `internal/host/executor.go`, when `ctx.Done()` fires, the executor returns an error:

```go
case <-ctx.Done():
    return "", ctx.Err()
```

The engine then sees a non-nil error and fails the run entirely, bypassing the timeout wire.

## Fix

Convert context deadline exceeded into a `"timeout"` result string, not an error. This applies to all step execution paths (script, agent, human):

```go
case <-ctx.Done():
    return "timeout", nil
```

The engine will then follow the `timeout` wire, which the parser already implicitly binds to `abort` when not explicitly declared (implemented in cloche-x1av).

## Files to change

- `internal/host/executor.go` — all `ctx.Done()` branches in step execution
- Verify `internal/engine/engine.go` handles the `"timeout"` result correctly and does not treat it specially beyond wire routing

Ref: docs/design/human-in-the-loop.md — Timeout section
