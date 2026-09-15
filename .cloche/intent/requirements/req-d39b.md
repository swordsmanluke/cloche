---
id: req-d39b
status: active
scope:
    level: domain
    domains:
        - daemon-host-orchestration
        - intent-continuity
hints:
    - host script running in the wrong git worktree
    - assuming host workflow scripts always run from the main worktree
    - MainWorktreeDir fallback behavior
    - script needs to know which git tree it's operating in
    - host script cwd when the project directory is a linked worktree
    - does CLOCHE_PROJECT_DIR equal the main git worktree
    - project dir is a linked worktree, which directory do I commit in
    - why does the intent-scan commit step use CLOCHE_PROJECT_DIR instead of cwd
confidence: high
user_edited: false
provenance:
    kind: commit
    ref: 07037c2d90061aecffc75f83a052e89c537dfade
    extracted_at: 2026-09-15T21:21:18Z
    extracted_by: intent-scan
created: 2026-09-15T21:22:37.980657481Z
updated: 2026-09-15T21:22:37.980657481Z
---

CLOCHE_PROJECT_DIR is the project directory, not necessarily the main git worktree — the two differ whenever the project directory is itself a linked worktree. A host-workflow script/poll step's working directory defaults to the main git worktree, but this is a fallback default, not a guaranteed invariant: `MainWorktreeDir()` falls back to the project directory on any error (not a git repository, `git` unavailable, or `git worktree list` failing), and `Executor.scriptDir()` falls back to `ProjectDir` when `MainDir` is unset. Code that needs a specific directory (e.g. intent-scan's `commitScript`, which must commit in the same directory `apply-reconcile` just wrote to) should reference `CLOCHE_PROJECT_DIR` explicitly, or otherwise resolve the directory it needs at runtime, rather than relying on cwd defaults or assuming the two coincide.

**Why:** req-1584 already captured the 'main-worktree cwd is a default, not an invariant' insight from docs/workflows.md. Commit 07037c2 applied that same principle inside intent-scan's own commitScript (internal/intent/scan/builtin.go, intent-continuity domain) after a comment there wrongly claimed CLOCHE_PROJECT_DIR 'is the main git worktree' for intent-scan runs. Since req-1584's scope was daemon-host-orchestration only, it wouldn't have been injected into intent-continuity prompts, so this broadens scope to cover both domains — the same class of mistake recurred in a second domain.
