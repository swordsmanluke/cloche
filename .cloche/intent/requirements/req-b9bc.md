---
id: req-b9bc
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - I disabled a requirement and a later scan brought it back
    - does intent-scan ever delete a requirement file
    - a scan re-enabled something I had turned off
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-09-13-intent-continuity-design.md
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.455653614Z
updated: 2026-09-14T15:33:28.455653614Z
---

A `disabled` intent requirement is never re-enabled by a scan, and a scan never deletes a requirement file outright — `superseded` is the only terminal state a scan can assign to an existing requirement.

**Why:** Enforced as a hard, non-LLM-judged validation rule during reconcile: if the user explicitly disabled a requirement, a duplicate candidate found later is simply dropped, preserving the user's decision.
