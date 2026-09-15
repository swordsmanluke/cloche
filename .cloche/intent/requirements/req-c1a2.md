---
id: req-c1a2
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
hints:
    - why didn't cloche commit my changes
    - dispatch failing on dirty worktree
    - task branch has no commits after dispatch
    - worktree has staged files at pickup
confidence: high
user_edited: false
provenance:
    kind: prompt
    ref: e3bn-main
    extracted_at: 2026-09-15T00:38:02Z
    extracted_by: intent-scan
created: 2026-09-15T00:41:34.461544869Z
updated: 2026-09-15T00:41:34.461544869Z
---

Cloche never authors git commits on a task branch itself — that stays the workflow's (agent/script step's) responsibility — and dispatch fails if the task worktree already has modified or staged tracked files, with a per-step opt-out.

**Why:** Keeps commit authorship under the workflow's own control and prevents dispatch from silently starting work on a dirty tree.
