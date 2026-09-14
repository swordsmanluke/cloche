# Intent Tracking: Built-in Migration Design

**Date:** 2026-09-14
**Status:** Proposed
**Prior:** [`2026-09-13-intent-continuity-design.md`](2026-09-13-intent-continuity-design.md),
implemented via cloche-56iz…cloche-0vqg.

## Problem

The v1 implementation deliberately kept half the feature outside the engine as a
safety measure while it was unproven: the `intent-scan` workflow, its three prompts,
and its glue scripts live in *this repo's* `.cloche/` directory. That made cloche
its own test harness — but it means a project that adopts cloche gets injection,
retrieval, CLI, and dashboard for free, yet cannot extract requirements until it
hand-copies workflow files. Extraction is also opt-in (`intent.scan_after_tasks`,
default false).

The feature is proven (dogfooded on this repo: 29 requirements extracted, injection
live). Migrate the rest into cloche itself: **extraction and injection are
automatic, default-on, disabled per-step with `intent_tracking = false`.**

## Changes

### 1. Built-in workflow mechanism + embedded `intent-scan`

New engine concept: **built-in workflows**, constructed in Go and registered in the
daemon, resolved by name *after* project workflow discovery — a project defining a
workflow with the same name overrides the built-in (same override semantics the
original design promised). `cloche workflow list` / the dashboard DAG mark them
`(built-in)`.

`intent-scan` becomes the first built-in:

- The workflow graph is defined in Go (no DSL file); the three agent prompts move
  to `internal/intent/scan/prompts/*.md` via `go:embed`, resolved without touching
  the project filesystem.
- The script steps disappear as scripts: collect-sources and apply-reconcile
  already live in the binary (`cloche intent collect-sources` /
  `apply-reconcile`); the built-in workflow invokes those commands directly.
- This repo's `.cloche/host.cloche` `intent-scan` block, its prompts, and its
  scripts are **deleted** in the same change — cloche dogfoods the built-in from
  day one.

`changelog` stays project-local for now; the mechanism is generic so it can follow
later if wanted.

### 2. Automatic extraction

- `intent.scan_after_tasks` default flips to **true**. After a task's main run
  completes, the daemon enqueues an incremental scan (existing trigger path in
  `grpc/server.go`), now hitting the built-in workflow so it works in any project.
- First scan in a project bootstraps `.cloche/intent/` (domain discovery runs on
  the initial pass, as designed). A project that wants none of it sets
  `intent.scan_after_tasks = false` — and with no `.cloche/intent/` dir, injection
  stays dormant exactly as before.
- Guardrail: the auto-scan is skipped when another intent-scan for the project is
  already queued or running (no pile-ups on busy loops).

### 3. `intent_tracking` step/workflow key (rename + umbrella)

The v1 opt-out `intent = "off"` (string) is replaced by **`intent_tracking =
false`** (boolean), accepted at step and workflow level. Default true. No
deprecation alias: the feature has not shipped in a release yet, so we fix the
name before first release. `intent.inject = "off"` in config.toml remains the
project-wide injection kill-switch.

`intent_tracking = false` is an umbrella:

- **Injection** — the step's prompt gets no requirements block (today's behavior
  of `intent = "off"`).
- **Extraction** — the step is excluded from scan mining: its transcript and
  prompt are filtered out by collect-sources (new), so steps handling
  noise/sensitive material don't seed requirements.

### 4. Docs

`docs/intent.md` and `docs/workflows.md` updated: built-in workflow concept,
automatic scanning, `intent_tracking`, and removal of the copy-the-workflow setup
instructions.

## Sequencing / versioning

All of this lands **before** the pending minor release, so v3.21.0 ships the
feature in its final shape (automatic, built-in, `intent_tracking`). Individual
slices are build bumps; the already-planned minor bump covers the whole feature.
The `intent = "off"` → `intent_tracking` rename is only release-safe because no
release has shipped the old key.

## Tickets

- **A (`cloche-la93`) — Built-in workflow mechanism; embed intent-scan; delete project-local copy**
  (engine: registry + name resolution + DAG/CLI labeling; `go:embed` prompts;
  workflow graph in Go; remove `.cloche/` intent-scan block/prompts/scripts).
- **B (`cloche-nbnh`) — Automatic extraction** (flip `scan_after_tasks` default; concurrency
  guard; bootstrap path verified on a fixture project with no `.cloche/intent/`).
  Depends on A.
- **C (`cloche-hxws`) — `intent_tracking` key** (parse boolean at step+workflow level; wire to
  injection gate; collect-sources exclusion; remove `intent = "off"`). Parallel
  with A.
- **D (`cloche-cg8a`) — Docs + release alignment** (docs updates; confirm minor bump ships after
  A–C). Depends on A, B, C.
