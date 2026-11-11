---
id: req-3ba7
status: active
scope:
    level: domain
    domains:
        - signals
hints:
    - infinite loop of follow-up tasks
    - handler failed silently
    - repeat failure task creation
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: ae4503b9d2a521ec38296434d8b53c8386d6169c
    extracted_at: 2026-11-11T17:52:17.679367653Z
    extracted_by: intent-scan
created: 2026-11-11T17:52:17.745817487Z
updated: 2026-11-11T17:52:17.745817487Z
---

Don't file a find-out-why task about a find-out-why task; failures of the signal handler workflow itself must be recorded as runs so they are visible.

**Why:** Prevents runaway recursive task creation from failing handlers.
