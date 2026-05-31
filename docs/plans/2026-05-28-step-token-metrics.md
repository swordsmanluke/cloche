# Per-Step Token Metrics Design

**Date:** 2026-05-28
**Status:** Draft — open questions to be resolved in L1 and L2

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

`runs` carries `workflow` (the workflow name) and `task_id`.

So the raw data exists; what is missing is a query/reporting layer that can pivot on
`workflow + step` as the metric identity key.

## Scope

This document designs the **query and reporting layer** for per-step token data.
Implementation of the metric persistence layer (whether to denormalise into a
separate `metrics` table or query `step_executions` directly) is the core decision
in L1. The query shapes and CLI surface are specified in L2.

**Out of scope:**
- Web dashboard panel (deferred to a future ticket)
- OpenTelemetry / external alerting
- Cost-in-dollars tracking (tokens are the stable unit)
- Code-quality metrics (`cloche-metric` binary — separate design)

## Open Questions

These questions must be resolved before implementation. L1 resolves the identity and
schema decisions; L2 resolves the query and CLI surface.

### OQ-1 · Canonical step identifier (L1)

What string uniquely identifies a step series across runs?

**Option A — flat:** `"<workflow>/<step>"` (e.g., `develop/implement`)
- Simple. Ambiguous if two workflows have a step with the same name.

**Option B — fully qualified:** `"<workflow>:<subworkflow>:<step>"` (e.g., `main:implement-vertical-layer:implement`)
- Verbose. Matches the run-state-step-view design's proposed identifier format.
- Must coordinate with `wrapped_cloche-uoq` (run-state per-step view).

**Decision needed:** Pick a format, document it, and ensure both tickets use the same key.

### OQ-2 · Storage strategy (L1)

**Option A — query `step_executions` directly:**
- No schema changes. Query joins `runs` for workflow name.
- May be slow for trend queries spanning many runs.

**Option B — denormalise into a separate `metrics` table:**
- Matches the `docs/plans/2026-05-26-metrics-reporting.md` proposal (if/when that
  document exists) for a unified metrics table.
- Requires a migration and a write hook at step completion.

**Decision needed:** Pick an approach. If Option B, specify the table schema and the
migration.

### OQ-3 · Host-vs-container prompt step coverage (L1)

Container `prompt` steps flow through `internal/adapters/agents/prompt/prompt.go` and
return a `StepResult` with `Usage` populated. Do **host** `prompt` steps (steps inside
a `host { }` workflow block) go through the same path and emit `TokenUsage`?

**Decision needed:** Confirm yes/no with a reference to the code path. Document any
gap (e.g., host-prompt steps that use a different adapter).

### OQ-4 · Three query shapes (L2)

Specify the concrete SQL or API shape for:

1. **Slice by step** — token breakdown for one `workflow/step` pair.
2. **Aggregate by workflow** — total tokens per step within a workflow, sorted descending.
3. **Trend over time** — tokens per step per day/week window.

Each shape should have a worked SQL example against the chosen storage strategy.

### OQ-5 · CLI surface (L2)

`cloche metrics` and `clo metric` are proposed in the metrics-reporting design.
Specify:
- The exact subcommand / flag forms for step-level token queries.
- Output format (table, JSON, CSV).
- Reconcile with any existing `cloche status --tokens` output.

### OQ-6 · Implementation hook point (L2)

Where in the codebase should token emission be wired for the query layer?
Candidate: `internal/adapters/agents/prompt/prompt.go` at step completion, where
`StepResult.Usage` is already available. Confirm or propose an alternative.
