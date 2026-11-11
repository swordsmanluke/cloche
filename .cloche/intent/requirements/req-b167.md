---
id: req-b167
status: active
scope:
    level: project
hints:
    - X-Forwarded-For loopback check
    - how to detect local request
    - trusting proxy headers
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: cmpo-main
    extracted_at: 2026-11-11T18:06:36.451418704Z
    extracted_by: intent-scan
created: 2026-11-11T18:06:36.666581482Z
updated: 2026-11-11T18:06:36.666581482Z
---

Decide whether a dashboard request is local from the connection's remote address, never from a forwarded header.

**Why:** Forwarded headers can be spoofed by remote clients to bypass the token requirement.
