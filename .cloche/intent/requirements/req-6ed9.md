---
id: req-6ed9
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - bind target ambiguous
    - module and workflow have the same name
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md
    extracted_at: 2026-10-15T16:23:02.761300226Z
    extracted_by: intent-scan
created: 2026-10-15T16:23:02.806430995Z
updated: 2026-10-15T16:23:02.806430995Z
---

A bare bind target must not be ambiguous between a module and a project workflow; validation rejects the ambiguity.

**Why:** Prevents a bind silently resolving to the wrong workflow.
