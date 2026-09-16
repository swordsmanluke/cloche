---
id: req-e416
status: active
scope:
    level: domain
    domains:
        - self-hosting-workflows
hints:
    - my uncommitted changes disappeared between task runs
    - where should I do experimental or in-progress work in this repo
    - safe place for work in progress in the cloche repo
    - dev loop wiped my changes in the main working tree
confidence: medium
user_edited: false
provenance:
    kind: commit
    ref: 6799ef7ebe9c375effa9efb5e313357fd10ecc2b
    extracted_at: 2026-09-16T20:00:00Z
    extracted_by: intent-scan
created: 2026-09-16T19:58:52.347675676Z
updated: 2026-09-16T19:58:52.347675676Z
---

This project's own primary working tree is subject to routine `git reset`/`git clean` housekeeping between the autonomous dev loop's task cycles; uncommitted work left there instead of committed or moved to a separate worktree/branch can be silently destroyed between cycles.

**Why:** An earlier uncommitted attempt at an experimental workflow file was lost this way when it lived in-tree; the working fix was to do that work in its own git worktree/branch instead of the main working tree.
