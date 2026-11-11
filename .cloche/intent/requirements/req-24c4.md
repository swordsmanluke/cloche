---
id: req-24c4
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - result success of step is not wired
    - cloche validate unwired result
    - what results can a module workflow return
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/modules.md#Contracts
    extracted_at: 2026-11-11T21:04:22.815792323Z
    extracted_by: intent-scan
created: 2026-11-11T21:04:22.868522211Z
updated: 2026-11-11T21:04:22.868522211Z
---

Module contract results are only `success` (reached done) and `fail` (reached abort); a step calling a module workflow must wire every result it can produce, or `cloche validate` reports the result as not wired.

**Why:** Contracts are checked statically.
