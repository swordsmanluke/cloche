---
id: req-ff9a
status: superseded
superseded_by: req-32fa
scope:
    level: project
hints:
    - new web handler needs auth
    - container cannot reach daemon http
    - where are tokens checked
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: cmpo-main
    extracted_at: 2026-11-11T18:06:36.451418704Z
    extracted_by: intent-scan
created: 2026-11-11T18:06:36.666750261Z
updated: 2026-11-11T19:53:14.541190879Z
---

Dashboard auth is enforced by one middleware on the mux, not per-handler checks; the per-project signal token and per-run /mcp token stay separate and unchanged.

**Why:** A per-handler check can be missed on new routes; run containers call only /mcp from a container address, not loopback.
