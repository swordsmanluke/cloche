# Edge Selection, Fanout, and Collect

**Date:** 2026-02-20
**Status:** Approved

## Problem

The current engine maps step outcomes to edges using only exit codes: exit 0
maps to the first declared result, non-zero maps to `"fail"`. This limits steps
to two possible edges and provides no mechanism for a step to select from
multiple named edges at runtime. The engine also executes steps sequentially
with no support for parallel branches.

## Design

### Result Protocol

Steps communicate their chosen edge via a stdout marker:

```
CLOCHE_RESULT:<name>
```

The adapter scans captured stdout for the **last** line matching this prefix.
The marker line is stripped from the output written to `.cloche/output/<step>.log`.

**Fallback (convention mode):** If no marker is found, the exit code determines
the result:
- Exit 0 → `"success"`
- Non-zero → `"fail"`

The marker always takes precedence over exit code. A step that emits
`CLOCHE_RESULT:needs_research` but exits non-zero will route to `needs_research`,
not `fail`.

**Undeclared or unwired results abort the workflow.** If a step produces a result
that is not in its declared `results` list, or that has no matching wire or
collect condition, the engine treats it as a fatal error and aborts the run.
There is no silent fallback.

### Step Result Declarations

Steps declare whatever results they need. There are no mandatory results.

```
# Convention mode — exit code handles everything
step test {
    run = "bundle exec rake test 2>&1"
    results = [success, fail]
}

# Custom edges — LLM must emit marker for non-standard results
step analyze {
    prompt = file("prompts/analyze.md")
    results = [success, fail, needs_research]
}

# No standard edges — always requires explicit marker
step triage {
    prompt = file("prompts/triage.md")
    results = [bug_fix, feature_request, needs_clarification]
}
```

For `triage`: if the LLM exits 0 without a marker, the engine resolves
`"success"`, finds it not declared, and aborts. This forces the step to be
explicit.

### Fanout

Multiple wires from the same `(step, result)` pair trigger parallel execution:

```
code:success -> test
code:success -> lint
```

When `code` reports `success`, both `test` and `lint` start concurrently.
There is no implicit ordering between parallel branches.

### Collect

A `collect` keyword in the wiring section joins parallel branches:

```
collect all(test:success, lint:success) -> merge
collect any(test:success, lint:success) -> quick_check
```

- `all`: target step starts when every listed condition is satisfied.
- `any`: target step starts when any one condition is satisfied.

**Unsatisfied collects:** If a collected step reports a result that doesn't
match its condition (e.g. `test` reports `fail` instead of `success`), that
condition is never satisfied. If the collect can never be fully satisfied, it
simply never fires. The run continues along whatever branches are still active.

**`collect any` does not cancel other branches.** All parallel branches run to
completion. A future `collect any cancel(...)` variant could add cancellation
semantics.

**Fanout and collect compose naturally:**

```
code:success -> test
code:success -> lint

collect all(test:success, lint:success) -> merge
test:fail -> fix
lint:fail -> fix

merge:success -> done
```

### DSL Grammar Extension

```
wiring_entry  = simple_wire | collect_wire
simple_wire   = ident ":" ident "->" ident
collect_wire  = "collect" collect_mode "(" condition_list ")" "->" ident
collect_mode  = "all" | "any"
condition_list = condition ("," condition)*
condition     = ident ":" ident
```

### Domain Model Changes

```go
type Wire struct {
    From   string
    Result string
    To     string
}

type Collect struct {
    Mode       CollectMode
    Conditions []WireCondition
    To         string
}

type CollectMode string

const (
    CollectAll CollectMode = "all"
    CollectAny CollectMode = "any"
)

type WireCondition struct {
    Step   string
    Result string
}

type Workflow struct {
    Name      string
    Steps     map[string]*Step
    Wiring    []Wire
    Collects  []Collect
    EntryStep string
}
```

`NextStep(name, result) (string, error)` is replaced by
`NextSteps(name, result) ([]string, error)` returning all wire targets for
the given pair.

### Engine Rewrite

The engine changes from a linear loop to a concurrent DAG walker.

**State:**

```go
type engineState struct {
    active    map[string]context.CancelFunc  // currently running steps
    pending   map[string]*collectState       // collects waiting for conditions
    completed map[string]string              // step name -> result
}

type collectState struct {
    collect   *domain.Collect
    satisfied map[int]bool  // which conditions have been met
}
```

**Execution cycle:**

1. Start with `EntryStep`.
2. When a step completes with a result:
   - Record in `completed`.
   - Look up simple wires via `NextSteps` → launch targets concurrently.
   - Check all pending collects → mark satisfied conditions, fire any that
     are fully met.
3. Terminate when no steps are active and no collects can still fire.

**Concurrency:** Each step runs in its own goroutine. A shared mutex protects
engine state. Step completion is communicated via a channel.

**Termination:** The run succeeds if all branches reach `done`. It fails if any
branch reaches `abort` or any step execution errors. Dead branches from
unsatisfied collects are not an error — the run outcome depends on the
branches that did complete.

**Max steps** is a total across all branches.

### Adapter Changes

**Shared result extraction** in `internal/protocol/result.go`:

```go
func ExtractResult(output []byte) (result string, cleanOutput []byte, found bool)
```

Scans for the last `CLOCHE_RESULT:` line, returns the result name and the
output with marker lines removed.

**Generic adapter:** Captures stdout via `bufio.Scanner` on a pipe instead of
`CombinedOutput()`. Calls `ExtractResult`. Falls back to exit code convention.

**Prompt adapter:** Appends result selection instructions to the assembled
prompt, listing all declared results:

```
When you are finished, output exactly one of the following on its own line:
CLOCHE_RESULT:success
CLOCHE_RESULT:fail
CLOCHE_RESULT:needs_research
```

After the LLM exits, scans output with `ExtractResult`. Same fallback.

**`resultOrDefault` is removed.** Adapters return exactly one of: the marker
value, `"success"`, or `"fail"`. The engine validates against declared results
and wiring.

### Validation

Parse-time validation expands:

- Every declared result must be wired (simple wire or collect condition).
- Every result referenced in a wire or collect must be declared by its step.
- Collect target steps must exist.
- Collect conditions must reference existing steps.
- Standard reachability checks still apply.

### Backward Compatibility

Existing workflows using `pass`/`fail` must rename `pass` to `success` and
update their wiring accordingly. This is the only breaking change.

Workflows that only use `success` and `fail` results continue to work via
exit-code convention with no stdout markers needed. `collect` is purely
additive syntax — workflows without it parse and run identically.
