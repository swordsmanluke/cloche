# 5. How Changes Land

The agent works on a *copy* of your project inside the container. Getting its
changes back onto your branch is the daemon's job plus one generated script —
you never create branches or worktrees yourself.

## What the daemon does automatically

Around every container sub-workflow (the `develop` step in `main`):

1. **Before**: creates a branch `cloche/<attempt>-<container>` and a matching
   git worktree at `.gitworktrees/cloche/<suffix>`, and publishes the branch
   name in the KV store as `child_branch` (plus `child_run_id`).
2. **After**: copies the container's `/workspace` into that worktree and
   commits it on the branch.
3. **On success**: removes the worktree. **On failure**: keeps it, so you can
   inspect exactly what the agent produced.

So by the time the `merge` step runs, the agent's work is already a committed
branch in your repo.

## What merge.py does

`.cloche/scripts/merge.py` consumes that branch — it never creates worktrees:

1. Reads `child_branch` from the KV store and locates the daemon's worktree.
2. Publishes `worktree_path` and `base_branch` (for the fix-merge prompt).
3. Rebases the branch onto your current branch inside the worktree.
4. Fast-forwards your branch to the rebased result, then deletes the
   worktree and branch.

## When the rebase conflicts

`merge` fails, and the workflow routes to **fix-merge** — an agent step whose
prompt is templated with `{{ $worktree_path }}` and `{{ $base_branch }}`
(tutorial 4). The agent restarts the rebase in the preserved worktree,
resolves the conflicts, completes the rebase, and reports success; then
`merge` re-runs and finds the rebase already done. If the agent can't
resolve it, the workflow unclaims the task and stops the loop for a human.

`cleanup.py` runs at the end of every successful pass and removes anything
left over (worktree, branch) — it's safe to run twice.

## Debugging a failed run

The worktree is kept on failure. Look under `.gitworktrees/cloche/` and
`git log cloche/<suffix>` to see what the agent committed. `cleanup.py` (or
`git worktree remove --force` + `git branch -D`) clears it once you're done.

**Next:** [6. Swapping the task tracker](6-swapping-the-task-tracker.md)
