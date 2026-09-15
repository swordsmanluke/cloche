---
id: req-3a6b
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - adding a delete-all-containers button to the dashboard
    - should the Containers view link to the daemon-wide cleanup endpoint
    - bulk container cleanup across projects in the UI
    - why isn't DELETE /api/containers used by the dashboard
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/web-dashboard.md#secondary-views-workflows-intent-containers
    extracted_at: 2026-09-15T17:55:00Z
    extracted_by: intent-scan
created: 2026-09-15T17:54:46.767451966Z
updated: 2026-09-15T17:54:46.767451966Z
---

The dashboard's Containers view never exposes a cross-project clean-up control, even though the daemon-wide `DELETE /api/containers` endpoint exists. Only per-task and per-project (within the currently active project) container deletion are wired into the UI.

**Why:** A single click on a daemon-wide clean-up button could silently remove another project's kept containers.
