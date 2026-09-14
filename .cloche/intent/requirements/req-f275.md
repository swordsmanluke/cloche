---
id: req-f275
status: active
scope:
    level: project
hints:
    - finalize failed to push, the base branch moved
    - can I force push to recover a failed merge
    - main branch out of sync after a failed finalize run
    - two runs finalizing into main at the same time
confidence: high
user_edited: false
provenance:
    kind: doc
    ref: docs/run-isolation/architecture.md
    extracted_at: 2026-09-14T00:00:00Z
    extracted_by: intent-scan
created: 2026-09-14T15:33:28.45390984Z
updated: 2026-09-14T15:33:28.45390984Z
---

The `finalize` step never force-pushes the base branch. It rebases the completed stack onto the latest base in a throwaway worktree and pushes with a fast-forward-only refspec; if the base moved again in the meantime the push is rejected and finalize must be re-run to rebase onto the new base.

**Why:** The base branch must never be checked out locally or overwritten by a run — conflicts can only land on a feature branch, and main only ever advances by fast-forward, so concurrent runs can't corrupt it.
