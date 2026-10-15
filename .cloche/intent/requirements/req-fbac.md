---
id: req-fbac
status: active
scope:
    level: domain
    domains:
        - agent-adapters
hints:
    - step has no result marker
    - agent wrapped CLOCHE_RESULT in backticks
    - unknown result from agent step
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 0ac281318069c3255e43ad70293f2621651862aa
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.45523728Z
updated: 2026-10-15T15:54:45.45523728Z
---

CLOCHE_RESULT markers are detected anywhere in a line (not only at line start), the result name is the run of identifier characters after the prefix, the last marker in the output wins, and a bare prefix with no name is not a marker.

**Why:** Agents deep in long contexts wrap the marker in backticks or bold or add prose around it; line-start-only matching misclassified such steps as having no marker.
