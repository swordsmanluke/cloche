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

echo "Cleaned up run $RUN_ID"
