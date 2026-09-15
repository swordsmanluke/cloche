---
id: req-3a23
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - a sensitive or noisy step's output became a requirement
    - does intent_tracking=false also stop scan mining
    - opted-out step still shows up in a scan
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/intent.md#opting-out
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461369808Z
updated: 2026-09-15T00:41:34.461369808Z
---

A step or workflow with intent_tracking = false also has its logs excluded from collect-sources transcript mining, not just from prompt injection.

**Why:** The opt-out is meant to cover both directions — noisy or sensitive step output should never seed a requirement either.
