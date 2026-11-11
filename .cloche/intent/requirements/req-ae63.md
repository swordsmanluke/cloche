---
id: req-ae63
status: active
scope:
    level: domain
    domains:
        - dras
        - daemon-host-orchestration
        - usage-budget
hints:
    - DRA created a task but it isn't picked up
    - can a DRA mark a task ready
    - DRA update refused for another domain's task
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/docs/dras.md#Daemon access (the wake-up credential)
    extracted_at: 2026-11-11T19:36:14.872604854Z
    extracted_by: intent-scan
created: 2026-11-11T19:36:14.930263681Z
updated: 2026-11-11T19:36:14.930263681Z
---

Tasks created by a DRA always carry the DRA's domain label; a DRA may update or close only tasks bearing that label, cannot remove its own label, and cannot set the `ready` label (a person promotes the task).

**Why:** `ready` hands a task to the loop, which spends budget; whether DRAs may set it is an open ruling, so the safe default is refuse.
