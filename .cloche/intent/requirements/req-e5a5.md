---
id: req-e5a5
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - will the retrieval hints show up in the agent's prompt
    - what is the hints field in a requirement file for
    - do hints get injected into the standing requirements block
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/plans/2026-09-13-intent-continuity-design.md
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.456004266Z
updated: 2026-09-14T15:33:28.456004266Z
---

A requirement's retrieval `hints` (short symptom-phrased strings in its frontmatter) are embedded for semantic search but are never rendered into the injected `## Standing project requirements` prompt block shown to agents.

**Why:** Hints exist purely to widen the embedding's surface for retrieval — the spike found them the single biggest quality lever for selection — while the injected block itself must stay compact and only show the statement/rationale an agent should act on.
