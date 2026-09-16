---
id: req-44c7
status: active
scope:
    level: domain
    domains:
        - intent-continuity
        - self-hosting-workflows
hints:
    - bd init --graph fails
    - how to bulk-create bd tasks from a graph file
    - bd create --graph unknown flag
    - scripting bd to build a dependency graph
    - hallucinated bd CLI flags
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 3cd9a87d6f5a07712d43b8ec9b98da283251750d
    extracted_at: 2026-09-16T15:18:21Z
    extracted_by: intent-scan
created: 2026-09-16T15:20:04.829977078Z
updated: 2026-09-16T15:20:04.829977078Z
---

The `bd` CLI has no bulk graph-import mode (no `bd init --graph` or `bd create --graph <file>`); a task graph must be built by creating each node individually with `bd create` and wiring dependencies afterward with `bd dep add`.

**Why:** The arm driver's first pilot run against the real bd CLI found the `--non-interactive`/`--graph` flags it used were hallucinated; there is no bulk-create mode, so nodes and edges must be created one at a time using the ids bd itself assigns.
