---
id: req-222c
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - attention_count is stale or slow to update
    - should we compute needs-you items per request
    - adding a new per-project count to the dashboard API
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/web-dashboard.md#JSON API
    extracted_at: 2026-09-15T18:30:00Z
    extracted_by: intent-scan
created: 2026-09-15T18:24:37.102009796Z
updated: 2026-09-15T18:24:37.102009796Z
---

Per-project attention data (`attention_count`, the "Needs you" item list) is served from a background-refreshed cache (`internal/attention.Cache`), not recomputed on every request.

**Why:** Keeps frequently-polled endpoints like `GET /api/projects` and `GET /api/projects/{name}/attention` cheap for the dashboard's polling tab bar and task stack, instead of recomputing attention state on each request.
