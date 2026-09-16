---
id: req-b8c5
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - intent scan fails with reconcile.json not found
    - automatic post-task scan keeps failing
    - reconcile step succeeded but didn't write its output file
    - does reconcile need a none result like collect-sources
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 63104e0db7631a322da2e79b2da89fb2f648c13a
    extracted_at: 2026-09-16T20:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T19:58:52.347198202Z
updated: 2026-09-16T19:58:52.347198202Z
---

The intent-scan `reconcile` step emits a `none` result (wired straight to `done`, skipping `apply-reconcile` and `commit`) when the `extract` step found zero candidates, instead of leaving `reconcile.json` unwritten and letting `apply-reconcile` fail on a missing file.

**Why:** The reconcile agent legitimately has nothing to write when extract found no durable intent, but before this fix that indistinguishable-looking case (agent reports success without writing reconcile.json) was silently treated as a step failure, causing the automatic post-task intent scan to fail repeatedly with no clear cause.
