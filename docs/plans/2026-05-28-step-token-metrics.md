# Per-Step Token Metrics Design

**Date:** 2026-05-28
**Status:** Design — complete

## Problem

Cloche already captures `input_tokens` and `output_tokens` per agent step execution
(stored in `step_executions`), but the query layer exposes only aggregate burn-rate
summaries grouped by agent. There is no way to ask:

- "How many tokens did the `implement` step use across the last 10 runs of `develop`?"
- "Which step in the `main` workflow is the biggest token consumer this week?"
- "Did the `fix` step get cheaper after I shortened its prompt?"

These questions require a metric query layer keyed by workflow + step, supporting
three access patterns: slice by step, aggregate by workflow, and trend over time.

## Existing State

`step_executions` in the SQLite store already carries:

| Column          | Type    | Notes                        |
|-----------------|---------|------------------------------|
| `run_id`        | TEXT    | FK to `runs`                 |
| `step_name`     | TEXT    | Step name (not qualified)    |
| `input_tokens`  | INTEGER | 0 when step has no usage     |
| `output_tokens` | INTEGER | 0 when step has no usage     |
| `agent_name`    | TEXT    | `"claude"`, `"codex"`, etc.  |
| `completed_at`  | TEXT    | ISO-8601 timestamp           |

`runs` carries `workflow_name`, `project_dir`, and `task_id`.

So the raw data exists; what is missing is a query/reporting layer that can pivot on
`workflow + step` as the metric identity key.

## Scope

This document designs the **query and reporting layer** for per-step token data.
The query layer reads `step_executions` directly — no new write path, no separate
metrics table.

**Out of scope:**
- Web dashboard panel (deferred to a future ticket)
- OpenTelemetry / external alerting
- Cost-in-dollars tracking (tokens are the stable unit)
- Code-quality metrics (`cloche-metric` binary — separate design)

---

## Metric Identity

**Decision (OQ-1): `workflow:step`** (colon-separated).

The identity key for a token-metric series is `runs.workflow_name + ":" +
step_executions.step_name`. Examples:

| workflow_name | step_name   | identity key          |
|---------------|-------------|-----------------------|
| `develop`     | `implement` | `develop:implement`   |
| `develop`     | `fix`        | `develop:fix`         |
| `main`        | `develop`    | `main:develop`        |

This aligns with the fully qualified step identifier in
`docs/plans/2026-05-28-run-state-step-view.md`, which proposes
`workflow:subworkflow:step` for deeply nested runs. Nested subworkflow runs produce
separate `step_executions` rows under separate `runs` records — each queryable
independently by its own `workflow:step` key. Callers attributing cost across
nested invocations join on `runs.parent_run_id`.

---

## Storage

**Decision (OQ-2): query `step_executions` directly.** No separate metrics table,
no new write path.

`SaveCapture` already writes `input_tokens` and `output_tokens` at step completion.
The general-purpose `metrics` table proposed in the metrics-reporting design
(`metrics(id, project, scope_type, scope_id, name, value, timestamp)`) is for
user-emitted custom metrics and other numeric primitives; step-token data has richer
native structure (agent name, run linkage, timestamp) and does not belong there.

### Required indexes

Add to `migrate()` in `internal/adapters/sqlite/store.go` (both idempotent):

```sql
-- Efficient per-project, per-workflow join for slice and aggregate queries.
CREATE INDEX IF NOT EXISTS idx_runs_project_workflow
    ON runs (project_dir, workflow_name);

-- Efficient step + time range scan for trend queries.
CREATE INDEX IF NOT EXISTS idx_step_executions_step_time
    ON step_executions (step_name, completed_at);
```

---

## Host vs Container Coverage

**Decision (OQ-3): both host and container prompt steps are covered.**

Both use `internal/adapters/agents/prompt/prompt.go:Adapter.Execute()`, which
returns `domain.StepResult{Usage: ...}`:

- **Container steps**: `internal/agent/session.go:executeStep` receives the result
  and sends it back via gRPC. The daemon calls `SaveCapture` with the usage.
- **Host steps**: `internal/host/runner.go:hostStatusHandler.OnStepComplete`
  receives `usage *domain.TokenUsage` from the engine and calls `SaveCapture`.

Token extraction and persistence are identical for both locations. Non-prompt steps
(script, `workflow_name` dispatch, human/poll) produce zero tokens, which is correct.

---

## Query Shapes

All three shapes join `step_executions se` to `runs r` on `se.run_id = r.id`.

### Shape 1 — Slice by step

```sql
SELECT
    r.workflow_name,
    se.step_name,
    COUNT(*)                                AS run_count,
    SUM(se.input_tokens)                    AS total_input,
    SUM(se.output_tokens)                   AS total_output,
    SUM(se.input_tokens + se.output_tokens) AS total_tokens
FROM step_executions se
JOIN runs r ON se.run_id = r.id
WHERE (? = '' OR r.project_dir   = ?)   -- :project_dir
  AND r.workflow_name = ?               -- e.g. 'develop'
  AND se.step_name    = ?               -- e.g. 'implement'
  AND (? = '' OR se.completed_at >= ?)  -- :since  (ISO-8601)
  AND (? = '' OR se.completed_at <= ?)  -- :until  (ISO-8601)
GROUP BY r.workflow_name, se.step_name;
```

Example — `develop:implement` over the last 30 days:

```
workflow   step        runs  input    output   total
develop    implement   47    843,210  194,880  1,038,090
```

### Shape 2 — Aggregate by workflow

```sql
SELECT
    se.step_name,
    COUNT(*)                                AS run_count,
    SUM(se.input_tokens)                    AS total_input,
    SUM(se.output_tokens)                   AS total_output,
    SUM(se.input_tokens + se.output_tokens) AS total_tokens
FROM step_executions se
JOIN runs r ON se.run_id = r.id
WHERE (? = '' OR r.project_dir  = ?)    -- :project_dir
  AND r.workflow_name = ?               -- e.g. 'develop'
  AND (? = '' OR se.completed_at >= ?)  -- :since (ISO-8601)
  AND (se.input_tokens + se.output_tokens) > 0
GROUP BY se.step_name
ORDER BY total_tokens DESC;
```

Example — workflow `develop`:

```
step        runs  input    output   total
implement   47    843,210  194,880  1,038,090
test        47    121,400   28,600    150,000
fix          9     82,100   19,700    101,800
```

### Shape 3 — Trend over time

```sql
SELECT
    se.step_name,
    strftime('%Y-%m-%d', se.completed_at) AS day,
    COUNT(*)                                AS run_count,
    SUM(se.input_tokens)                    AS total_input,
    SUM(se.output_tokens)                   AS total_output,
    SUM(se.input_tokens + se.output_tokens) AS total_tokens
FROM step_executions se
JOIN runs r ON se.run_id = r.id
WHERE (? = '' OR r.project_dir   = ?)   -- :project_dir
  AND r.workflow_name = ?               -- e.g. 'develop'
  AND se.step_name    = ?               -- e.g. 'implement'
  AND (? = '' OR se.completed_at >= ?)  -- :since (ISO-8601)
GROUP BY day
ORDER BY day ASC;
```

Example — `develop:implement` over the past week:

```
day         runs  input   output  total
2026-05-22   6    107k    25k     132k
2026-05-23   8    141k    33k     174k
2026-05-24   7    118k    27k     145k
2026-05-27   9    153k    35k     188k
2026-05-28   5     84k    19k     103k
```

---

## CLI Surface

**Decision (OQ-5):** Reconciled with the `cloche metrics` / `clo metric` proposal
in the metrics-reporting design.

### `cloche metrics` — step-token queries (host CLI)

```
# Slice: tokens for develop:implement
cloche metrics --workflow develop --step implement

# Slice with time filter
cloche metrics --workflow develop --step implement --since 2026-05-01

# Aggregate: all steps in workflow, sorted by total tokens
cloche metrics --workflow develop

# Trend: day-by-day breakdown for one step
cloche metrics --workflow develop --step implement --trend

# JSON output
cloche metrics --workflow develop --format json

# All projects (default: current directory)
cloche metrics --workflow develop --all
```

Accepted flags on all forms:

| Flag              | Default    | Notes                                  |
|-------------------|------------|----------------------------------------|
| `--workflow <w>`  | required   | Workflow name                          |
| `--step <s>`      | (all)      | Omit for aggregate-by-workflow shape   |
| `--since <date>`  | all time   | ISO-8601 (e.g. `2026-05-01`)           |
| `--until <date>`  | now        | ISO-8601                               |
| `--trend`         | false      | Group by day instead of totals         |
| `--format`        | `table`    | `table`, `json`, or `csv`              |
| `--all`           | false      | Query across all projects              |

### Relationship to `cloche status` burn-rate

`cloche status` shows an hourly total aggregated by agent name — live rate.
`cloche metrics` answers "which step cost what, over what window" — historical
analysis. No changes to the existing `status` output are needed.

### `clo metric` — not used for step-token data

`clo metric <name> <value>` is for _user-emitted_ custom metrics from inside steps.
Step-token data is _auto-emitted_ by the prompt adapter; `clo metric` has no role
in this flow.

---

## Implementation Notes

**Decision (OQ-6): the implementation is purely a read-side addition.**

No new write path is needed. `SaveCapture` already writes the token columns. The
emit points (`session.go:executeStep` for containers, `runner.go:OnStepComplete`
for host) are correct as-is.

### Where to add code

1. **`internal/adapters/sqlite/store.go`** — Add `QueryStepTokens(ctx,
   StepTokenQuery) ([]StepTokenSummary, error)`. `StepTokenQuery` holds
   `ProjectDir`, `WorkflowName`, `StepName`, `Since`, `Until`, `Trend bool`.
   Execute the appropriate shape above based on populated fields.

2. **`internal/ports/store.go`** — Add `MetricsStore` interface with
   `QueryStepTokens`. Wire alongside `CaptureStore` in daemon DI. Keeping it
   separate signals a read-only query port.

3. **`internal/adapters/grpc/server.go`** — Add `GetStepMetrics` RPC calling
   `QueryStepTokens`. Both the `cloche` CLI and any future web handler go through
   this RPC, not SQLite directly.

4. **`cmd/cloche/main.go`** — Add a `metrics` subcommand calling `GetStepMetrics`.
   Reuse the existing `formatTokenCount` helper for table output.

5. **`internal/adapters/sqlite/store.go` — indexes** — Add
   `idx_runs_project_workflow` and `idx_step_executions_step_time` to `migrate()`.

### Version bump

**Minor bump** (e.g., v3.16.0). New capability, no breaking change to any existing
API, CLI flag, DSL syntax, or proto message. `GetStepMetrics` is purely additive.
Old daemons return gRPC "unimplemented"; the CLI surfaces this as "upgrade daemon".
