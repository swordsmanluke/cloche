---
id: req-d5dd
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - cli
        - project-config
hints:
    - doctor warns credentials not set
    - module requires binary missing
    - validate fails on missing module credential
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 835088679d064946b6a9f3f2350d85e488cf8eb3
    extracted_at: 2026-10-15T16:16:50.445762526Z
    extracted_by: intent-scan
created: 2026-10-15T16:16:50.494635184Z
updated: 2026-10-15T16:16:50.494635184Z
---

Module validation treats a missing required binary on PATH as an error (also failing `cloche doctor`), but an unset declared credential is only a warning, never fatal.

**Why:** Credentials may legitimately be supplied at runtime via CLOCHE_EXTRA_ENV, so their absence in the current environment is not proof of misconfiguration; missing binaries always are.
