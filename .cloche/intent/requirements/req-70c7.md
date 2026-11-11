---
id: req-70c7
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
        - self-hosting-workflows
hints:
    - writing a stub for an external CLI
    - tests pass but the real command fails
    - SAGA_BIN stub
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: 8mfg-main
    extracted_at: 2026-11-11T21:04:22.815792323Z
    extracted_by: intent-scan
created: 2026-11-11T21:04:22.868859459Z
updated: 2026-11-11T21:04:22.868859459Z
---

The agent container has no `saga` binary, so stubs of host CLIs used in module tests must mimic the real CLI's contract (required flags, output shape, exit codes) and be updated when the real CLI is checked against; passing tests on a loose stub prove nothing.

**Why:** Saga-judge tests passed against a stub while the real CLI failed.
