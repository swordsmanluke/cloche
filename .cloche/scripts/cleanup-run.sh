#!/usr/bin/env bash
# cleanup-run.sh — Remove the worktree and branch left over from a cloche run.
# Expects CLOCHE_PREV_OUTPUT to point to a file containing the run ID.
set -euo pipefail

if [ -z "${CLOCHE_PREV_OUTPUT:-}" ] || [ ! -f "$CLOCHE_PREV_OUTPUT" ]; then
  echo "error: CLOCHE_PREV_OUTPUT not set or file missing" >&2
  exit 1
fi

RUN_ID=$(cat "$CLOCHE_PREV_OUTPUT" | tr -d '[:space:]')
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"

# Try to find the task ID from the ready-tasks output in our run's output directory
if [ -z "${CLOCHE_TASK_ID:-}" ]; then
  OUTPUT_DIR="$(dirname "$CLOCHE_PREV_OUTPUT")"
  READY_OUT="$OUTPUT_DIR/ready-tasks.out"
  if [ -f "$READY_OUT" ]; then
    CLOCHE_TASK_ID=$(jq -r '.[0].id // empty' "$READY_OUT" 2>/dev/null) || true
  fi
fi
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

# Close the task if CLOCHE_TASK_ID is set
if [ -n "${CLOCHE_TASK_ID:-}" ]; then
  bd close "$CLOCHE_TASK_ID" 2>/dev/null && echo "Closed task $CLOCHE_TASK_ID" || echo "warning: could not close task $CLOCHE_TASK_ID" >&2
fi

echo "Cleaned up run $RUN_ID"
