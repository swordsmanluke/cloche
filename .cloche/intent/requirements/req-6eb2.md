---
id: req-6eb2
status: active
scope:
    level: domain
    domains:
        - container-runtime
hints:
    - extraction refused too many deletions
    - file deleted after run but agent never touched it
    - clocheignore tracked file removed
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: c91ec59e1dcf9df1d9ebf1c1c038d9e0f035dd94
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.454931432Z
updated: 2026-10-15T15:54:45.454931432Z
---

Extraction sorts staged deletions by provenance: deliberate deletions (recorded in the container's git index relative to BaseSHA) always land; tracked files excluded by .clocheignore are restored from BaseSHA rather than committed as deletions; only unattributed deletions are subject to the mass-delete refusal heuristic.

**Why:** A blanket deletion-count gate refused legitimate refactors that git rm'd subsystems, while silently committing .clocheignore'd files as deletions.
