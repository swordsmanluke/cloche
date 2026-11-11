---
id: req-e32c
status: active
scope:
    level: domain
    domains:
        - dras
        - dashboard-auth
hints:
    - how does a dra authenticate to the daemon
    - token for container to create tasks
    - credential expiry after run ends
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: wvjx-main
    extracted_at: 2026-11-11T19:27:03.94283836Z
    extracted_by: intent-scan
created: 2026-11-11T19:27:03.994591315Z
updated: 2026-11-11T19:27:03.994591315Z
---

A DRA's daemon credential is issued per wake-up, tied to the DRA's name and project, valid only while that run lives, and modelled on the per-run MCP token.

**Why:** A DRA needs more than a run but much less than the CLI; it must not hold the project's signal token (rulings CLOCHE-A118, A119, A128).
