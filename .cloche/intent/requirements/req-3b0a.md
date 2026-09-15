---
id: req-3b0a
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
hints:
    - should I file this ticket now even though it isn't ready to start
    - draft ticket got claimed and started before its prerequisites landed
    - how to stage follow-on work without it being picked up immediately
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: d84725e
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.46344326Z
updated: 2026-09-15T00:41:34.46344326Z
---

Draft or exploratory bead tickets for planned-but-not-ready work should be kept out of bead entirely, not even filed as blocked/draft, until their prerequisite work is finished and verified.

**Why:** The orchestration loop claims open tickets within seconds of creation, so a ticket filed before it's actually ready to start will get picked up prematurely.
