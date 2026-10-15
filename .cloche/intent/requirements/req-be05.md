---
id: req-be05
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
hints:
    - git stash list has dozens of entries
    - stash pop conflict after merge
    - scan-state.yaml conflict on merge
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 41d294ab4919547595a5e8128083bdb49b927451
    extracted_at: 2026-10-15T15:54:45.394708619Z
    extracted_by: intent-scan
created: 2026-10-15T15:54:45.455802197Z
updated: 2026-10-15T15:54:45.455802197Z
---

merge-to-base restores local uncommitted state around its fast-forward by popping the stash, resolving a conflicted pop in favour of the stashed (local) side and dropping the entry; a pop that fails before applying anything is left for a human.

**Why:** The daemon rewrites .cloche/intent/scan-state.yaml, so pops routinely conflicted and orphaned one stash per merge (65 accumulated).
