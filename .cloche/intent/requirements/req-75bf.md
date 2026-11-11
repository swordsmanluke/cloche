---
id: req-75bf
status: active
scope:
    level: domain
    domains:
        - workflow-dsl
hints:
    - judge step crashed what result
    - interactive saga question judged
    - judge failure handling
confidence: medium
user_edited: false
provenance:
    kind: doc
    ref: docs/modules.md
    extracted_at: 2026-11-11T19:21:22.374725056Z
    extracted_by: intent-scan
created: 2026-11-11T19:21:22.42585603Z
updated: 2026-11-11T19:21:22.42585603Z
---

The saga-judge judge step always ends done/success: a failed judge just leaves questions to the human, and only workflow-asked questions are judged, never interactive-session ones.

**Why:** Failure of the judge must degrade to human review, never block or auto-resolve.
