---
id: req-cfba
status: active
scope:
    level: domain
    domains:
        - intent-continuity
        - daemon-host-orchestration
hints:
    - should this commit step use git add -A
    - auto-commit step picked up unrelated working tree edits
    - writing a new git-committing host workflow step
    - daemon commit swept in a file I hadn't finished editing
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/intent.md#extraction-cloche-intent-scan
    extracted_at: 2026-09-15T20:05:05Z
    extracted_by: intent-scan
created: 2026-09-15T20:06:41.001526823Z
updated: 2026-09-15T20:06:41.001526823Z
---

A host-workflow step that auto-commits daemon-written files into the main worktree (e.g. intent-scan's commit step) must scope `git add`/`git commit` strictly to the paths it wrote, and must never run a bare `git add -A` or `git commit -a`.

**Why:** The main worktree may legitimately hold unrelated in-progress user edits at the moment the step fires; a wildcard add/commit would sweep those up into an automated commit that isn't the human's to make.
