---
id: req-c703
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
        - cli
hints:
    - cloche loop fails with a nested project error
    - running the orchestration loop from a subdirectory
    - duplicate task assignment across nested projects
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#cloche loop
    extracted_at: 2026-09-16T12:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T19:12:00.069522195Z
updated: 2026-09-16T19:12:00.069522195Z
---

cloche loop refuses to run if the project directory is nested inside another project directory that already has an active orchestration loop; run the loop from the outermost project's directory instead.

**Why:** Two loops over overlapping directory trees would race to claim and run the same tasks, causing duplicate task contention.
