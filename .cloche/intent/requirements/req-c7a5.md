---
id: req-c7a5
status: active
scope:
    level: domain
    domains:
        - web-dashboard
hints:
    - dashboard port already in use on daemon startup
    - daemon fails to bind http port
    - should the daemon retry or give up when the dashboard port is taken
    - cloche health shows dashboard down after restart
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/web-dashboard.md#enabling-the-dashboard
    extracted_at: 2026-09-15T17:55:00Z
    extracted_by: intent-scan
created: 2026-09-15T17:54:46.767667423Z
updated: 2026-09-15T17:54:46.767667423Z
---

If the dashboard's configured HTTP port is unavailable when the daemon starts, the daemon does not give up — it retries the bind with exponential backoff (1s up to 30s) until it succeeds or the daemon shuts down. While down, the dashboard's status is surfaced via `cloche status` and `cloche health` instead of the daemon just failing to connect.

**Why:** Handles the common case of a port still held by a previous daemon that hasn't fully exited, without requiring the whole daemon to fail to start.
