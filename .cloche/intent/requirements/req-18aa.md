---
id: req-18aa
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - which model runs the A/B experiment
    - should I use bonsai or a local model for the experiment
    - change the experiment executor
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: 'docs/plans/spikes/2026-09-17-intent-ab-pilot/README.md#Decision: frozen executor for replications (2026-10-15)'
    extracted_at: 2026-10-15T15:57:31.706145393Z
    extracted_by: intent-scan
created: 2026-10-15T15:57:31.754259749Z
updated: 2026-10-15T15:57:31.754259749Z
---

claude-haiku-4-5-20251001 is the frozen executor for intent A/B replications; do not invest further in bonsai-8b-16k harnesses or add small-model tiebreakers.

**Why:** Three independent harnesses failed before scoring and changing executor would invalidate the frozen seed-v2.1 corpus and collected control runs.
