# Per-Step Token Metrics

**Date:** 2026-05-28
**Status:** Design

## Problem

Token usage is already captured per agent step — `domain.TokenUsage`
(`InputTokens`/`OutputTokens`) is populated in `internal/agent/session.go`
and rolled into a `UsageSummary` with a burn rate. But the data is
computed on-read and never persisted as a durable, queryable series. There
is no way to:

- slice token usage **by step**,
- aggregate it **by workflow**, or
- **compare over time** (this week vs. last, run vs. run).

## Requirements

- Record **input and output tokens** for each prompt step, each run.
- Identity key is **workflow name + step name** — that pair is sufficient
  for uniqueness across the metric series.
- Support querying that:
  - slices by step (e.g. tokens for `develop:implement` across runs),
  - aggregates by workflow (sum/avg per workflow),
  - trends over time (compare windows).

## Relationship to existing work

This is a concrete sub-spec of the general metrics framework in
[2026-05-26-metrics-reporting.md](2026-05-26-metrics-reporting.md), which
already lists "Token usage" as an auto-emitted default metric scoped by
step. That doc proposes the storage (a `metrics` SQLite table keyed by
`name` + `scope` + `timestamp`) and CLI surface. This capture pins down the
specific identity (`workflow + step`) and the three slice/aggregate/trend
query shapes that the token metric must support.

## Step Identifier

The canonical step identity for storage and querying is the **flat two-field
pair `(workflow_name, step_name)`**, unqualified.

When a step belongs to a subworkflow, the `workflow_name` field holds that
subworkflow's name (e.g. `develop`), and `step_name` holds the step within it
(e.g. `implement`). There is no concatenated FQN in the stored key.

**Reconciliation with run-state-step-view:** The run-state step-view feature
([2026-05-28-run-state-step-view.md](2026-05-28-run-state-step-view.md))
displays steps as fully-qualified `workflow:subworkflow:step` strings for UI
readability. That FQN is a **render-time derivation** from the run hierarchy —
it is not stored. The metric storage key and the display string are different
things; they agree on the components (`workflow_name` + `step_name`) but the
display layer joins them with `:` at query time, not at write time.

**Uniqueness:** Within a project, the pair `(workflow_name, step_name)` is
unique per metric series. Adding `run_id` scopes a record to a specific
execution. The full identity key for a single row is
`(project_dir, workflow_name, step_name, run_id, timestamp)`.

## Schema

Token usage is stored in the `metrics` table introduced by the general
metrics framework. The per-step token metric rows have the following fields:

```
metrics (
  id            INTEGER PRIMARY KEY,
  project_dir   TEXT    NOT NULL,          -- absolute path; ties metric to a project
  workflow_name TEXT    NOT NULL,          -- owning workflow (e.g. "develop")
  step_name     TEXT    NOT NULL,          -- step within workflow (e.g. "implement")
  run_id        TEXT    NOT NULL,          -- ties metric to a specific execution
  name          TEXT    NOT NULL,          -- always "token_usage" for this metric type
  input_tokens  INTEGER NOT NULL,          -- prompt / cache-read tokens consumed
  output_tokens INTEGER NOT NULL,          -- completion tokens generated
  timestamp     TEXT    NOT NULL           -- RFC 3339 UTC, recorded at step completion
)
```

Index covering time-range and aggregation queries:

```sql
CREATE INDEX metrics_step_time
  ON metrics (project_dir, workflow_name, step_name, timestamp);
```

**Value types:** `input_tokens` and `output_tokens` are `INTEGER` (SQLite
64-bit int). The `domain.TokenUsage` struct already carries them as `int64`.

**`name` field:** Always `"token_usage"` for rows written by this feature.
Future metric types (duration, success rate) will use different values in the
same table, keeping aggregation queries simple.

**Nullability:** A row is only written when token usage is available — script
steps and human steps produce no token data and emit no row.

## Host vs Container

Both host-side and container-side prompt steps emit `domain.TokenUsage`.

**Container steps** (`internal/agent/session.go`): The `prompt.Adapter.Execute`
call returns a `domain.StepResult` with a non-nil `Usage` field. `session.go`
packages it into `pb.TokenUsage` and sends it to the daemon via the
`AgentSession` gRPC stream. The daemon's `StepResult` handler persists it.

**Host steps** (`internal/host/executor.go`): `executeAgent` calls
`prompt.Adapter.Execute` directly and returns the full `domain.StepResult`
including `Usage`. The `hostStatusHandler.OnStepComplete` receives the usage
value and passes it to `captures.SaveCapture`. Coverage is complete.

**Gap — script and human steps:** `StepTypeScript` and `StepTypeHuman` steps
execute shell commands or wait for human input; neither calls into the LLM and
neither produces `TokenUsage`. This is expected: there is no gap to document
for these types.

**Gap — `run:` agent steps:** An `agent:` step with a `run:` key dispatches a
sub-workflow via `genericAdapter` rather than `promptAdapter`, so it does not
produce per-step token data at the dispatch level. Token data is captured
inside the sub-workflow's own steps instead. This is the correct behavior.

## Storage

Token metrics are stored in the `metrics` table defined in
[2026-05-26-metrics-reporting.md](2026-05-26-metrics-reporting.md). That doc
already describes the table's schema and indexing strategy; this feature
populates it with rows whose `name` field is `"token_usage"`.

The `metrics` table is the canonical home for all auto-emitted observability
data in Cloche. This feature does not introduce a new table.

## Query Shapes

The three canonical query shapes this feature must support, with concrete SQL
examples against the `metrics` table:

### Slice-by-step

Return per-run token totals for a specific step:

```sql
-- slice-by-step: tokens for develop:implement across all runs
SELECT run_id, input_tokens, output_tokens, timestamp
FROM metrics
WHERE project_dir = '/path/to/project'
  AND workflow_name = 'develop'
  AND step_name     = 'implement'
ORDER BY timestamp DESC;
```

### Aggregate-by-workflow

Sum tokens across all steps in a workflow for each run:

```sql
-- aggregate-by-workflow: total tokens per run for the 'develop' workflow
SELECT run_id,
       SUM(input_tokens)  AS total_input,
       SUM(output_tokens) AS total_output
FROM metrics
WHERE project_dir  = '/path/to/project'
  AND workflow_name = 'develop'
GROUP BY run_id
ORDER BY MIN(timestamp) DESC;
```

### Trend-over-time

Compare token totals across time windows:

```sql
-- trend-over-time: daily token totals for develop:implement
SELECT DATE(timestamp) AS day,
       SUM(input_tokens)  AS daily_input,
       SUM(output_tokens) AS daily_output
FROM metrics
WHERE project_dir  = '/path/to/project'
  AND workflow_name = 'develop'
  AND step_name     = 'implement'
  AND timestamp >= DATE('now', '-30 days')
GROUP BY day
ORDER BY day;
```

## CLI Surface

Token metrics are surfaced through the `cloche metrics` command family. The
specific command forms are:

```
cloche metrics tokens                          # summary table for all steps
cloche metrics tokens --step develop:implement # filter to one step
cloche metrics tokens --workflow develop       # aggregate by workflow
cloche metrics tokens --since 7d              # trend over last 7 days
```

Output is a tab-separated table suitable for terminal display. The
`--since` flag accepts durations (`7d`, `30d`, `1h`) and ISO date strings.
The `--step` flag uses the `workflow:step` display form and is resolved to
`(workflow_name, step_name)` at query time.
