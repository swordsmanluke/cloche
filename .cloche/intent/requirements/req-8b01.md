---
id: req-8b01
status: active
scope:
    level: domain
    domains:
        - project-config
hints:
    - duplicate project showing up after a daemon restart
    - wrong Dockerfile used for a build
    - project path points into .gitworktrees
    - extract worktree treated as its own project root
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: s5j3-main
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461926351Z
updated: 2026-09-15T00:41:34.461926351Z
---

Worktrees under .gitworktrees/ must never be discovered or registered as their own cloche projects by the daemon's project scan.

**Why:** A corrupted extract-worktree once got registered as its own project after a daemon restart, causing later builds/copies to use stale or wrong content.
