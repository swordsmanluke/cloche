# Per-Step Token Metrics

**Date:** 2026-05-28
**Status:** Captured (pre-design)

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

## Open questions

- Host steps vs. container steps — only prompt steps have token usage today;
  confirm host-side prompt steps emit it the same way.

*Resolved: Canonical step identifier — decided as `(workflow_name, step_name)`
pair, unqualified. A step is stored and queried as the two-field tuple — the
FQN display string (`workflow:subworkflow:step`) is derived at render time from
the run hierarchy. Consistent with
[2026-05-28-run-state-step-view.md](2026-05-28-run-state-step-view.md).*
