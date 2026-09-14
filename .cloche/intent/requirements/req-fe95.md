---
id: req-fe95
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - project has no intent directory, will prompts still work normally
    - is intent injection required for cloche to run
    - do I need to run intent-scan before using cloche at all
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/workflows.md#intent-injection
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.455887986Z
updated: 2026-09-14T15:33:28.455887986Z
---

A project with no `.cloche/intent/` directory gets no prompt injection and no warnings — the intent-continuity feature is fully dormant until the first scan creates that directory.

**Why:** Keeps the feature additive and safe to ship as a default-on behavior: existing projects that never run `intent scan` see no change at all.
