---
id: req-f0ad
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - can I add a third result to a module workflow
    - do I need to version a contract
    - how do I declare a named interface
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-10-13-modules-design.md#Non-goals (v1)
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.453330277Z
updated: 2026-10-15T15:54:45.453330277Z
---

Module compatibility is structural, not nominal: a workflow fits a call site if every input it declares is available there and it writes every key downstream steps read. There are no named, published or versioned contracts in v1, and the result set is fixed to success and fail.

**Why:** Standard contracts are documented shapes rather than registry objects; versioned/named contracts were explicitly rejected for v1.
