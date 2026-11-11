---
id: req-10c6
status: active
scope:
    level: domain
    domains:
        - usage-budget
hints:
    - usage reading failed
    - budget parse error blocks runs
    - /usage output format changed
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/USAGE.md#[budget]
    extracted_at: 2026-11-11T00:33:44.93352215Z
    extracted_by: intent-scan
created: 2026-11-11T00:33:44.981689593Z
updated: 2026-11-11T00:33:44.981689593Z
---

If the budget usage reading fails (e.g. the /usage output format changed), the failure is logged and shown in the Budget: line, and nothing is blocked (fail open).

**Why:** A parse failure must not halt all launches.
