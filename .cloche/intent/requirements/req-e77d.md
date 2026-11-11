---
id: req-e77d
status: superseded
superseded_by: req-1251
scope:
    level: project
hints:
    - adding a new dashboard POST route
    - is the dashboard endpoint authenticated
    - can anyone on the network stop or delete runs
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: cmpo-main
    extracted_at: 2026-11-11T18:06:36.451418704Z
    extracted_by: intent-scan
created: 2026-11-11T18:06:36.665756022Z
updated: 2026-11-11T19:53:14.541190879Z
---

Every mutating dashboard HTTP route requires the daemon-wide token (cookie or bearer, constant-time compare), from any address including loopback; read-only routes require it only when the connection's remote address is not this machine.

**Why:** Dashboard is often bound to 0.0.0.0; rulings CLOCHE-A131 and A132.
