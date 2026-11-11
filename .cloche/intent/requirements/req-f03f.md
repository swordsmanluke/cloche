---
id: req-f03f
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
        - self-hosting-workflows
hints:
    - main is failing tests
    - should I block merges when CI is red
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: 'docs/plans/2026-11-10-autonomous-loop-design.md#A red main: fix forward'
    extracted_at: 2026-11-11T00:16:38.456159878Z
    extracted_by: intent-scan
created: 2026-11-11T00:16:38.513240395Z
updated: 2026-11-11T00:16:38.513240395Z
---

A red `main` is fixed forward: merges are never paused when `main` goes red, because pausing could block the fix itself. The red-main signal is urgent and carries failing tests and the commit range since the last green check.

**Why:** Pausing merges could block the fix.
