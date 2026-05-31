# Run State: Per-Step View

**Date:** 2026-05-28
**Status:** Design

## Problem

The run-state web UI collapses subworkflow steps into a single entry. When a step
dispatches a subworkflow, the individual steps of that subworkflow aren't shown —
visibility into where time went and which step is currently running is lost.

## Solution

Add a flat per-step table to the Run Details Page
([docs/design/ui-pages.md](../design/ui-pages.md)). Every step in the run appears
as its own row, tagged with the workflow it belongs to.

## Row schema

| Column     | Description                                                            |
|------------|------------------------------------------------------------------------|
| `step`     | Fully-qualified step name: `workflow:subworkflow:step`                 |
| `result`   | Enumerated status: `success`, `running`, `failed`, `pending`, etc.     |
| `started`  | ISO-8601 timestamp (UTC)                                               |
| `duration` | Human-readable elapsed time (e.g. `1m 30s`); blank if not finished    |

### Canonical step identifier

The `step` field uses `:` as the separator: `main:dev:implement`. This is the
**canonical step identifier** shared with the per-step token metrics feature
([2026-05-28-step-token-metrics.md](2026-05-28-step-token-metrics.md)).

Full qualification (root workflow included) is always used. A root-level step of
workflow `main` appears as `main:build`, not just `build`.

## Data source

The table is populated from the existing run-state endpoint. The response needs a
flat `steps` array — the server flattens the execution tree before serialization:

```json
{
  "steps": [
    {
      "id": "main:dev:implement",
      "result": "success",
      "started": "2026-05-28T10:00:00Z",
      "duration_ms": 90000
    }
  ]
}
```

Subworkflow steps appear as ordinary rows. A flat representation is simpler than a
recursive/nested one and matches the display contract (flat table, no collapsing).

## Step ordering

**Chronological by `started` timestamp**, ascending (oldest first).

Rationale: the primary use case is debugging a live or completed run. Users want to
see what happened in time order. DAG/topological ordering would require maintaining
the full execution graph on the client; for sequential steps, chronological and
topological order are identical anyway, and for parallel steps the temporal ordering
is still meaningful and easier to reason about.

## Color assignment

Each distinct workflow segment (the root-most component of the qualified step name)
gets a stable color derived by `hash(workflow_name) mod 8`. The 8 colors are CSS
variables `--wf-color-0` through `--wf-color-7`, defined in `:root` and shared with
the palette introduced by the web-ui-cleanup work
([2026-05-26-web-ui-cleanup.md](2026-05-26-web-ui-cleanup.md)).

**Stability rule**: color is determined purely by workflow name, not by render order
or run. `main` is always the same color across runs and across page loads.

**Wrapping**: after 8 distinct workflow names, colors repeat (`mod 8`). Acceptable —
most runs involve fewer than 8 distinct workflows.

## Nesting depth

No indentation. The qualified step name (`main:dev:implement`) already encodes depth
in text. Adding indentation to a flat table creates layout complexity without enough
payoff for the common case.

Long qualified names truncate with `text-overflow: ellipsis`. The full name appears
on hover via the `title` attribute.
