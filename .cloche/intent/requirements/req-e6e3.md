---
id: req-e6e3
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - does intent scan --full re-scan everything from the start
    - how to force a full re-collection for intent scan
    - intent scan --full seems to do nothing different
    - reset scan-state.yaml cursors to re-mine old docs
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: 'docs/intent.md#Extraction: cloche intent scan'
    extracted_at: 2026-09-16T16:40:00Z
    extracted_by: intent-scan
created: 2026-09-16T16:40:44.718928459Z
updated: 2026-09-16T16:40:44.718928459Z
---

`cloche intent scan --full` does not currently reset collect-sources' cursors or force a full domain re-survey. It passes `--full` through as the run's task prompt, but no step reads it: collect-sources always mines only material newer than scan-state.yaml's cursors, and discover-domains decides full-vs-incremental purely by whether domains.yaml already exists. There is no supported way to force a full re-collection of a project's docs/commits/runs after the first scan has advanced past them, short of deleting .cloche/intent/scan-state.yaml (which also discards the doc-hash and scanned-run cursors).

**Why:** Documented as a known gap with a tracked follow-up — don't assume `--full` does anything beyond a plain incremental scan today.
