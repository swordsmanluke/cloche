# Fix Merge Conflicts

The merge step failed: rebasing the agent's result branch onto the base branch
produced conflicts. Everything you need is filled in below from the run's
key-value store via prompt template variables:

- Worktree with the conflicting branch checked out: {{ $worktree_path }}
- Base branch to rebase onto: {{ $base_branch }}

The previous rebase attempt was aborted, so start it again:

1. Run:

   git -C {{ $worktree_path }} rebase {{ $base_branch }}

2. Resolve each conflicted file semantically — understand both sides and
   integrate them; do not just pick one. After editing each file, run
   git -C {{ $worktree_path }} add <file>.

3. Complete the rebase:

   git -C {{ $worktree_path }} rebase --continue

Do not run rebase --abort, do not merge, and do not delete any branch — after
you report success the merge step re-runs and performs the fast-forward itself.

Report success when the rebase completes cleanly; report fail if the conflicts
cannot be resolved.

## Reporting your result (required)

When you are completely done, print exactly one of these markers as the final line of your output:

- `CLOCHE_RESULT:success` — the task is complete (and tests pass, where applicable)
- `CLOCHE_RESULT:fail` — you could not complete the task

An agent that exits without printing a marker is treated as failed, regardless of what the prose says.
