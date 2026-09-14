---
id: req-065f
status: active
scope:
    level: domain
    domains:
        - container-runtime
hints:
    - container has files from the wrong branch
    - why did my container's commit revert unrelated changes on main
    - container workspace doesn't match what's on main
    - run picked up a dirty working tree
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/run-isolation/architecture.md
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.454004659Z
updated: 2026-09-14T15:33:28.454004659Z
---

Containers must be seeded from a clean per-run git snapshot pinned at the run's base SHA (a fresh local clone checked out to that commit), never from the live shared project working tree.

**Why:** Host workflow steps mutate the shared working tree (git checkout, etc.); copying that tree live into a container would leak stale or dirty state into a commit that later gets extracted and merged back over main — this previously wiped unrelated committed code more than once.
