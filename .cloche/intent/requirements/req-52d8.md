---
id: req-52d8
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - can we just delete the old dashboard routes now
    - removing a deprecated console URL
    - how long should an old route keep redirecting before removal
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/web-dashboard.md#Routing
    extracted_at: 2026-09-15T18:30:00Z
    extracted_by: intent-scan
created: 2026-09-15T18:24:37.101869885Z
updated: 2026-09-15T18:24:37.101869885Z
---

Old console URL schemes (`/projects/{name}[/runs]`, `/runs[/{id}]`, `/tasks/{id}`, `/failed-tasks`) redirect into the new `/{project-slug}[/{task-id}]` routing scheme for one release before being removed, rather than being removed immediately.

**Why:** Gives clients/bookmarks a full release cycle to migrate to the new console routing scheme across the rework instead of breaking them immediately.
