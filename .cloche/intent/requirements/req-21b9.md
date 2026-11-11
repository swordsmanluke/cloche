---
id: req-21b9
status: active
scope:
    level: domain
    domains:
        - signals
        - web-dashboard
        - security-safety
hints:
    - signal endpoint returns 404 for unknown project
    - rotate signal token
    - should signals post be unauthenticated
    - trusting signal evidence text
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/docs/SAFETY.md#Signal Intake Token
    extracted_at: 2026-11-11T21:07:19.865683258Z
    extracted_by: intent-scan
created: 2026-11-11T21:07:19.967668314Z
updated: 2026-11-11T21:07:19.967668314Z
---

The signals endpoint compares the per-project bearer token in constant time and returns an identical 401 for an unknown project and a wrong token; signal text (title, evidence) is untrusted input.

**Why:** Distinct errors would let project names be probed; a signal creates a task that spends budget and steers an agent.
