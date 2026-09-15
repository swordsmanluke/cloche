---
id: req-3c12
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - my custom intent-scan workflow isn't being used
    - how do built-in workflows interact with project-defined ones
    - workflow name collides with a built-in
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-09-14-intent-builtin-migration-design.md
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461460309Z
updated: 2026-09-15T00:41:34.461460309Z
---

intent-scan is a built-in, Go-defined workflow resolved after project workflow discovery — a project can fully override it by defining its own workflow of the same name in a .cloche/*.cloche file.

**Why:** Lets built-in workflows ship with zero project setup while staying overridable per-project.
