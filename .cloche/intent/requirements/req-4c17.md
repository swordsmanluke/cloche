---
id: req-4c17
status: active
scope:
    level: domain
    domains:
        - intent-continuity
hints:
    - scan-state.yaml modified after a run
    - vectors.json shows up in git status
    - scan failed and left changes
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 7ee6d6a88b71669c29d9caec4aeec64adabb9c06
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.455666701Z
updated: 2026-10-15T15:54:45.455666701Z
---

An intent scan must never leave .cloche/intent or .cloche/intent-index dirty: `none` exits go through commit, every failure path goes through abort-cleanup which rewinds scan-state.yaml (so the window is rescanned) before aborting, and the embedding index dir is gitignored.

**Why:** Uncommitted scan state dirtied the working tree after every run and caused merge-time stash conflicts.
