#!/usr/bin/env bash
# cleanup-run.sh — Remove the worktree and branch left over from a cloche run.
# Reads child run ID and task ID from run context (set via cloche set).
set -euo pipefail

RUN_ID=$(cloche get child_run_id) || true
CLOCHE_TASK_ID=$(cloche get task_id) || true
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"

if [ -z "$RUN_ID" ]; then
  echo "warning: child_run_id not found in run context, skipping branch cleanup" >&2
else
  BRANCH="cloche/${RUN_ID}"
  WORKTREE_DIR="$PROJECT_DIR/.gitworktrees/cloche/$RUN_ID"

  # Remove the extraction worktree if it still exists
  if [ -d "$WORKTREE_DIR" ]; then
    git -C "$PROJECT_DIR" worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true
  fi

  # Prune any stale worktree bookkeeping
  git -C "$PROJECT_DIR" worktree prune 2>/dev/null || true

  # Delete the branch if it still exists (merge may have already removed it)
  if git -C "$PROJECT_DIR" rev-parse --verify "$BRANCH" >/dev/null 2>&1; then
    git -C "$PROJECT_DIR" branch -D "$BRANCH" 2>/dev/null || true
  fi
fi

# Close the task if task_id is set
if [ -n "$CLOCHE_TASK_ID" ]; then
  bd close "$CLOCHE_TASK_ID" 2>/dev/null && echo "Closed task $CLOCHE_TASK_ID" || echo "warning: could not close task $CLOCHE_TASK_ID" >&2
fi

echo "Cleaned up run ${RUN_ID:-unknown}"
