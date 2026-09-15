---
id: req-128f
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - scan changed a domain I customized
    - domain map got overwritten by intent scan
    - domains.yaml entry I edited was reset
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/intent.md#domain-map-format
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.460955574Z
updated: 2026-09-15T00:41:34.460955574Z
---

A scan never removes or rewrites a domain a human has marked user_edited: true in domains.yaml; it may only propose additions or description touch-ups to other domains.

**Why:** Preserves human curation of the domain map across incremental and full scans, mirroring the same protection already given to requirements.
