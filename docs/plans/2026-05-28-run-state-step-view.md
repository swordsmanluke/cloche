# Run State: Per-Step View

**Date:** 2026-05-28
**Status:** Design — data model complete

## Problem

The run-state web UI rolls everything inside a subworkflow up into a single
mass. When a step dispatches a subworkflow, the individual steps of that
subworkflow aren't shown — you lose visibility into where time went and
which step is currently running.

## Requirements

Show **every step** in the run, flat, each tagged with the workflow it
belongs to. Don't collapse subworkflow steps. Each row shows:

- **step** — fully qualified name, `workflow:subworkflow:step`
- **result** — success / running / failed / etc.
- **started** — ISO-8601 timestamp
- **duration**

Example:

```
step                 result     started     duration
main:dev:implement   success    iso-8601    10m
main:dev:test        running    iso-8601    2m
```

Use **color to distinguish each (sub)workflow segment** — e.g. `main` is one
color, `dev` another — so the eye can group steps by their owning workflow.

## Row Schema

Each row in the flat step table is a **view model** — derived from the API
response at render time, not stored as a new database table. The existing
`step_executions` SQLite table already holds all the source data.

| Field | Type | Description |
|---|---|---|
| `step_fqn` | TEXT | Fully-qualified display name, e.g. `main:dev:implement`. Derived at render time by prepending ancestor workflow names from the run hierarchy. |
| `result` | TEXT enum | One of: `success`, `running`, `failed`, `skipped`, `pending` — mirrors the values in `domain.RunState` / `StepExecution.Result`. |
| `started_at` | TEXT | ISO-8601 timestamp from `step_executions.started_at`. Empty for pending steps. |
| `duration` | TEXT | Human-readable elapsed time (e.g. `10m`, `2m30s`). Derived as `completed_at − started_at`; for running steps, computed as `now − started_at`. |
| `workflow_segment` | TEXT | The `workflow_name` of the run that owns this step (e.g. `dev`). Used to assign the per-workflow color band. Stable across rows — every step in a given subworkflow run shares the same segment tag. |

The `workflow_segment` tag drives the color grouping. It is the same as the
`WorkflowName` field on the `domain.Run` that owns the step — no new field is
needed on `StepExecution`.

## Canonical Step Identifier

**Decision: `(workflow_name, step_name)` pair, unqualified.**

The identity key is the two-field tuple `(workflow_name, step_name)` — the
workflow that defines the step plus the step's name within that workflow.
This is sufficient for uniqueness within a single run: a step name is unique
per workflow, and each run belongs to exactly one workflow.

Concretely, the step `implement` inside the `dev` subworkflow is identified as
`("dev", "implement")`, not as `"main:dev:implement"`. The fully-qualified
display string (`main:dev:implement`) is **derived at render time** by walking
the run hierarchy (each child run records `ParentRunID` and `ParentStepName`).

This matches the identity key in
[2026-05-28-step-token-metrics.md](2026-05-28-step-token-metrics.md), which
already uses `workflow name + step name` as the metric scope. Both features
share the same canonical identifier format — no reconciliation needed at the
storage level.

The existing `step_executions` table columns `run_id` + `step_name` already
encode this pair (the run's `workflow_name` is on the `runs` row). No schema
change is needed to implement this decision.

## Data Source

**Endpoint:** `GET /api/runs/{id}` — the existing run-detail endpoint.
**No new endpoint is required.**

### Response shape (relevant fields)

```jsonc
{
  "id": "run-abc",
  "workflow_name": "main",
  // ...
  "steps": [
    {
      "step_name": "dev",        // step name within this run's workflow
      "result": "success",
      "started_at": "2026-05-28T10:00:00Z",
      "completed_at": "2026-05-28T10:10:00Z",
      "duration": "10m0s",
      "run_id": "run-abc",       // which run owns this step
      "depth": 0,                // 0 = top-level, 1 = inside a child run, etc.
      "is_workflow": true,       // true when step dispatched a child run
      "parent_index": -1,        // flat-list index of parent workflow step
      "child_run_id": "run-xyz", // set when is_workflow=true
      "child_state": "succeeded"
    },
    {
      "step_name": "implement",
      "result": "success",
      "started_at": "2026-05-28T10:00:05Z",
      "duration": "9m55s",
      "run_id": "run-xyz",       // child run
      "depth": 1,
      "parent_index": 0
    }
    // ...
  ],
  "child_runs": [
    { "id": "run-xyz", "workflow_name": "dev", ... }
  ]
}
```

### How subworkflow steps are surfaced

The handler's `flattenRun` function (in
`internal/adapters/web/handler.go`) already flattens the tree: child-run
steps are inserted immediately after the workflow step that spawned them,
with `depth` incremented by 1. The client receives a **pre-flattened array**
— it does not need to recurse into a nested structure.

The `run_id` field on each step identifies which run (and therefore which
`workflow_name`) owns the step. The client derives `workflow_segment` by
looking up `run_id` in the `child_runs` array (or using the top-level run's
`workflow_name` for depth-0 steps). This is O(n) in the number of child
runs, acceptable for the sizes involved.

The fully-qualified display name for a step is assembled client-side by
collecting ancestor workflow names: for a step at depth 1, walk up via
`parent_index` to collect the top-level workflow name, then prepend to the
step name.

## Relationship to existing work

Extends the Run Details Page described in
[ui-pages.md](../design/ui-pages.md). This is a
**new feature** (a flat, per-step run view), distinct from the styling
cleanup in
[2026-05-26-web-ui-cleanup.md](2026-05-26-web-ui-cleanup.md) — though the
per-(sub)workflow coloring should reuse whatever palette/CSS-variable system
that cleanup settles on rather than introducing more inline styles.

## Color Assignment

Workflow segments are color-coded using CSS variables from the web-ui-cleanup palette
defined in [2026-05-26-web-ui-cleanup.md](2026-05-26-web-ui-cleanup.md). Each distinct
`workflow_segment` value maps to one of the segment variables:

| Segment index | CSS variable | Applies to |
|---|---|---|
| 0 | `--seg-0` | top-level run workflow |
| 1 | `--seg-1` | first subworkflow |
| 2 | `--seg-2` | second subworkflow |
| … | … | additional subworkflows |

**Stability rule:** colors are assigned by **depth-first traversal order** of the run
hierarchy and are stable within a single page view — a given `workflow_name` at a given
depth always receives the same segment index. If more than 6 distinct segments appear,
the index wraps (mod 6); a visual collision is acceptable at that scale.

## Step Ordering

**Chosen order: chronological by `started_at`.**

Steps are sorted ascending by their `started_at` timestamp. Steps without a `started_at`
(pending steps that have not yet been dispatched) are appended at the end in workflow-DSL
order.

**Rationale:** The flat step table is a timing view — the user wants to see what happened
first and what is taking the longest. Chronological order aligns with reading direction
and lets the user spot slow steps immediately. DAG/topological order would require
reconstructing the workflow graph client-side, adding complexity without meaningful
user benefit.

## Nesting Strategy

**Depth signaling: color (`workflow_segment`) plus indentation.**

Each row is indented by `depth × 1rem` to visually mirror the subworkflow hierarchy.
Color is the primary signal; indentation is the secondary signal for users who cannot
rely on color alone. Both are applied together.

**Long fully-qualified name handling:** The `step_fqn` cell uses CSS
`text-overflow: ellipsis` with `overflow: hidden; white-space: nowrap`. A tooltip
(`title` attribute) exposes the full name on hover. This prevents table layout
blow-out on deeply nested names such as `main:dev:implement:substep:leaf`.

## Open questions

*Resolved: Canonical step identifier — decided as `(workflow_name, step_name)`
pair; see Canonical Step Identifier section above. Consistent with
[2026-05-28-step-token-metrics.md](2026-05-28-step-token-metrics.md).*

*Resolved: Data source — existing `GET /api/runs/{id}` endpoint; no new
endpoint needed; see Data Source section above.*

*Resolved: Step ordering — chronological by `started_at`; see Step Ordering section.*

*Resolved: Color assignment — CSS variables from the web-ui-cleanup palette with
depth-first stability; see Color Assignment section.*

*Resolved: Nesting depth — color plus indentation; truncate with ellipsis and
tooltip for long names; see Nesting Strategy section.*
