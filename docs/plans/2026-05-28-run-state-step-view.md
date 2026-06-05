# Run State: Per-Step View

**Date:** 2026-05-28
**Status:** Design

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

### Columns

| Column | `apiStep` field | Type | Notes |
|--------|-----------------|------|-------|
| step | `QualifiedName` | string | Fully-qualified: `workflow:subworkflow:step` |
| result | `Result` | string | `success`, `running`, `failed`, `cancelled`, `stopped`, `skipped`; empty if pending |
| started | `StartedAt` | ISO-8601 UTC | Empty string if step has not started |
| duration | `Duration` | string | Human-readable (e.g. `10m0s`); `—` if not yet complete |
| *(derived)* | `WorkflowSegment` | string | Color-grouping key; not returned separately — client strips the last `:step` suffix from `QualifiedName` |

For step `main:dev:implement`, the `WorkflowSegment` is `main:dev`.
For top-level step `main:build`, it is `main`.

### Workflow rows

A step of type `StepTypeWorkflow` (one that dispatches a sub-workflow) is
included as its own row. Its `QualifiedName` is the workflow path without a
trailing step component (e.g. `main:dev`), its `result` reflects the
sub-workflow's terminal state, and it renders a collapse toggle consistent
with the existing tree view. Sub-workflow steps follow immediately after
their parent row in the DOM, so the indent relationship is preserved even
in a chronologically sorted list.

## Canonical Step Identifier

**Decision: fully-qualified name — `workflow:subworkflow:step`.**

- Top-level step `build` in workflow `main` → `main:build`
- Step `implement` in sub-workflow `dev` (dispatched from `main`) →
  `main:dev:implement`
- Deeper nesting follows: `main:dev:sub:step`

This is consistent with the per-step token metrics feature
([2026-05-28-step-token-metrics.md](2026-05-28-step-token-metrics.md)),
which also uses the full FQN as its identity key. The bare `workflow +
step` pair is ambiguous when nested workflows reuse step names; the full
qualification is required.

### Implementation: threading the prefix through `flattenRunFrom`

`flattenRunFrom` (`internal/adapters/web/handler.go`) is extended to
accept a `prefix string` parameter:

- Depth 0 (top-level run, workflow name = `"main"`): `prefix = "main"`
- Each step: `QualifiedName = prefix + ":" + StepName`
- When recursing into a child run (workflow name = `"dev"`):
  `childPrefix = prefix + ":" + childWorkflowName`

A new `QualifiedName string` field is added to `apiStep` and populated
during flattening. The existing `StepName` field (the local name within a
run) is preserved for log-fetch calls that already key on it.

## Data Source

**Endpoint:** `GET /api/runs/{id}` — no new endpoint required.

The existing response includes `steps []apiStep` produced by the recursive
`flattenRunFrom`. This feature extends `apiStep` with `QualifiedName` (see
above) and re-uses the same fetch that the tree view already makes.

Relevant response fragment:

```json
{
  "qualified_name": "main:dev:implement",
  "step_name":      "implement",
  "result":         "success",
  "started_at":     "2026-05-28T10:00:00Z",
  "completed_at":   "2026-05-28T10:10:00Z",
  "duration":       "10m0s",
  "depth":          1,
  "is_workflow":    false
}
```

The flat view and tree view share a single poll/fetch against `GET
/api/runs/{id}`. The flat view applies a client-side sort over the returned
`steps` array; no server-side sort change is needed.

## Step Ordering

**Decision: chronological by `started_at`, ascending (earliest first).**

The per-step view answers "what happened when?" — chronological order
matches that mental model directly. The existing tree view already exposes
the DAG structure; this view's distinct value is the time dimension.

### Edge cases

| Case | Handling |
|------|----------|
| Two steps share the same `started_at` | Stable secondary sort by `QualifiedName` (lexicographic ascending) |
| Pending steps (no `started_at`) | Grouped at the end, in DAG order (the order `flattenRun` produces them) |
| Workflow row vs. its children | Workflow row always sorts before its children — it started first by definition |
| Running step (no `completed_at`) | Sorted by `started_at` normally; `duration` displays as `—` |

### Sort location

The sort runs **client-side**, in the JavaScript that renders the flat view
tab, over the `steps` array from the existing endpoint. No server-side sort
change is needed. The flat view and tree view each independently apply
their own ordering to the same payload.

## Color Assignment

**Decision: stable, hash-based assignment — the same workflow segment
always maps to the same color index, across renders, page loads, and
runs.**

### Color key

The key is `WorkflowSegment` — the workflow path prefix derived from
`QualifiedName` by dropping the last colon-delimited component (e.g.
`main:dev` for steps owned by sub-workflow `dev`).

### Hash to index

```
colorIndex = stableHash(segment) % 6 + 1
```

`stableHash` sums the Unicode code points of the segment string. This is
computed client-side in JavaScript alongside the sort:

```js
function workflowColorIndex(segment) {
  let h = 0;
  for (const c of segment) h += c.codePointAt(0);
  return (h % 6) + 1;
}
```

No special-casing for `main` or any other name — the hash is applied
uniformly. The result is deterministic across all clients.

### CSS variables

Six named color slots are added to `:root` as part of the
[web-ui-cleanup](2026-05-26-web-ui-cleanup.md) palette additions. Hues
are derived from the existing badge palette (blue → `--badge-running-bg`,
green → `--badge-succeeded-bg`, amber → `--badge-pending-bg`) for visual
coherence:

```css
/* Light mode */
:root {
  --wf-1-border: #2563eb;   /* blue   */
  --wf-1-bg:     #eff6ff;
  --wf-2-border: #059669;   /* green  */
  --wf-2-bg:     #f0fdf4;
  --wf-3-border: #d97706;   /* amber  */
  --wf-3-bg:     #fffbeb;
  --wf-4-border: #7c3aed;   /* violet */
  --wf-4-bg:     #f5f3ff;
  --wf-5-border: #0891b2;   /* cyan   */
  --wf-5-bg:     #ecfeff;
  --wf-6-border: #e11d48;   /* rose   */
  --wf-6-bg:     #fff1f2;
}

@media (prefers-color-scheme: dark) {
  :root {
    --wf-1-border: #60a5fa;  --wf-1-bg: #1e3a5f;
    --wf-2-border: #34d399;  --wf-2-bg: #064e3b;
    --wf-3-border: #fbbf24;  --wf-3-bg: #78350f;
    --wf-4-border: #a78bfa;  --wf-4-bg: #4c1d95;
    --wf-5-border: #22d3ee;  --wf-5-bg: #164e63;
    --wf-6-border: #fb7185;  --wf-6-bg: #881337;
  }
}
```

### CSS classes

```css
.wf-1 { border-left: 3px solid var(--wf-1-border); background-color: var(--wf-1-bg); }
/* … through .wf-6 */
```

The `border-left` stripe and `background-color` tint together identify the
owning workflow. The tint is intentionally subtle so the result badge
(success/failed/running) remains the dominant visual signal.

### Palette exhaustion

Beyond 6 distinct workflow segments, color indices wrap (7 → slot 1, etc.).
Wrapping is acceptable: 7+ distinct workflow paths in one run is
uncommon in practice, and the `QualifiedName` text unambiguously identifies
the owning workflow regardless of color collision. The `title` attribute on
the colored row stripe (containing the workflow segment name) removes
residual ambiguity on hover.

## Nesting Strategy

**Decision: color and indent combined.** Color alone is insufficient once
two steps at different depths share the same workflow segment; indent
anchors each row to its depth in the call tree.

### Indent

A new CSS variable replaces the existing magic-number `calc()` inline
styles in `run_detail.html`:

```css
:root {
  --step-indent-unit: 1rem;   /* 16px */
}
```

The JavaScript renderer sets `paddingLeft` proportionally per row:

```js
row.style.paddingLeft = `calc(${depth} * var(--step-indent-unit))`;
```

Depth 0 (top-level) has no indent. Indent continues proportionally with no
max clamp — workflows deeper than 4 levels are unusual but handled
gracefully.

### Long qualified names

The step name cell is width-constrained. Names that overflow are truncated
with an ellipsis:

```css
.step-fqn {
  max-width: 30ch;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
```

`30ch` fits common names like `main:dev:implement` (20 chars) comfortably
while triggering truncation on degenerate names exceeding ~50 chars.

The full `QualifiedName` is always available on hover via the native HTML
`title` attribute set server-side in the template — no JavaScript tooltip
library is required.

### Workflow rows in the flat view

Workflow rows (`is_workflow = true`) display the workflow path as their
step name (e.g. `main:dev`, not `main:dev:dev`). They render the same
collapse toggle (▾/▸) as the existing tree view. Toggling hides/shows all
DOM rows with a matching `data-parent-index` via a `display` CSS flip — no
data re-fetch.

## Relationship to existing work

Extends the Run Details Page described in
[ui-pages.md](../../repos/cloche/docs/design/ui-pages.md). This is a
**new tab** on the existing Run Details page (`/project/<slug>/run/<id>`),
sitting alongside the current tree-view tab. The tree view is preserved
unchanged; the flat view is additive.

The per-step coloring reuses the CSS variable system defined by
[2026-05-26-web-ui-cleanup.md](2026-05-26-web-ui-cleanup.md). The
`--wf-N-border` / `--wf-N-bg` variables are added as part of that cleanup's
palette additions, not as inline styles. `--step-indent-unit` similarly
replaces existing magic-number `calc()` expressions in the current template.

The canonical step identifier (`workflow:subworkflow:step` FQN) is shared
with the token-metrics design
([2026-05-28-step-token-metrics.md](2026-05-28-step-token-metrics.md));
both features use the full qualification.

## How to verify this design

1. **Row schema / data source:** `apiStep` struct at
   `internal/adapters/web/handler.go:874` and `flattenRunFrom` at
   `:688` — confirm `QualifiedName` is the only new field needed.
2. **No CSS variable conflicts:** compare `--wf-*` names against the
   existing `:root` block in `internal/adapters/web/static/style.css:1`
   — none of those names exist today.
3. **Color hash:** `workflowColorIndex("main")` = (109+97+105+110) % 6
   + 1 = 421 % 6 + 1 = 2. `workflowColorIndex("dev")` = (100+101+118)
   % 6 + 1 = 319 % 6 + 1 = 2. These collide, which is intentional and
   acceptable — reviewers who prefer depth-seeded hashing may propose it
   as an alternative; this doc treats it as a deliberate trade-off.
