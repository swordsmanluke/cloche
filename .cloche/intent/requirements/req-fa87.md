---
id: req-fa87
status: active
scope:
    level: domain
    domains:
        - intent-continuity
        - daemon-host-orchestration
hints:
    - empty commit created by an automated step when nothing changed
    - git index lock contention during a host workflow commit
    - concurrent scan and merge step fighting over the git index
    - how to avoid junk commits from an automated host script
confidence: medium
user_edited: false
provenance:
    kind: prompt
    ref: runs/62qe-main/task_prompt.md
    extracted_at: 2026-09-15T20:05:05Z
    extracted_by: intent-scan
created: 2026-09-15T20:06:41.001685607Z
updated: 2026-09-15T20:06:41.001685607Z
---

A host-workflow commit step must no-op without creating an empty commit when it finds nothing to commit, and should retry a few times before failing when it hits git index-lock contention (e.g. a concurrent scan or a container-authored merge step also touching the main worktree) rather than leaving a half-staged index.

**Why:** With scans firing after every completed task, a no-guard commit step would produce junk empty commits on every quiet run, and two automated commits landing at once could otherwise corrupt the index instead of just retrying.
