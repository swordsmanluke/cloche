# Step Timeout Design

## Problem

Agent steps can hang indefinitely (e.g. Claude Code waiting on a plugin server
that doesn't exist in the container). The engine has no timeout mechanism, so
the run stays in "running" state forever.

## Design

### DSL Syntax

Per-step `timeout` key, parsed like any other config value:

```hcl
step implement {
    prompt = file(".cloche/prompts/implement.md")
    timeout = "45m"
    results = [success, fail]
}
```

Accepts any `time.ParseDuration` value (e.g. `"30m"`, `"1h"`, `"90s"`).

### Engine Changes

Add a `defaultTimeout` field to `Engine` (default 30m), settable via
`SetDefaultTimeout(d time.Duration)`.

In `launchStep`, wrap `executor.Execute` with a per-step context:

```go
go func(s *domain.Step) {
    stepCtx := ctx
    if d, ok := stepTimeout(s, e.defaultTimeout); ok {
        var cancel context.CancelFunc
        stepCtx, cancel = context.WithTimeout(ctx, d)
        defer cancel()
    }
    result, err := e.executor.Execute(stepCtx, s)
    results <- stepResult{stepName: s.Name, result: result, err: err}
}(step)
```

`stepTimeout(step, default)` returns the step's configured timeout or the
default. It parses `step.Config["timeout"]` via `time.ParseDuration`.

### Timeout Behavior

When the deadline fires:
1. `exec.CommandContext` kills the subprocess
2. `cmd.Run()` returns an error
3. The adapter returns `("fail", err)`
4. The engine routes via existing `fail` wiring

No new result type needed.

### Config Key Validation

Add a known-key registry to `Workflow.Validate()`. Each step's `Config` keys
are checked against a set of recognized keys:

```
prompt, run, max_attempts, timeout, agent_command, agent_args,
container.* (image, network_allow, memory, agent_command, agent_args)
```

Unrecognized keys produce a validation warning (logged to stderr), not a hard
error, to stay forward-compatible with future keys.

### Files Changed

| File | Change |
|------|--------|
| `internal/engine/engine.go` | `defaultTimeout` field, `SetDefaultTimeout`, `stepTimeout` helper, wrap Execute |
| `internal/engine/engine_test.go` | Test: step timeout produces "fail"; test: step config timeout overrides default |
| `internal/domain/workflow.go` | `ValidateConfig` method with known-key check, called from `Validate()` |
| `internal/domain/workflow_test.go` | Test: unknown config key produces warning |
