#!/usr/bin/env bash
# cleanup-run.sh — Remove the worktree and branch left over from a cloche run.
# The branch name lives in KV as child_branch (the daemon writes it when it
# pre-creates the extraction worktree). Falls back to constructing
# cloche/<child_run_id> for transition compatibility.
set -euo pipefail

PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
BRANCH=$(cloche get child_branch 2>/dev/null || true)
if [ -z "$BRANCH" ]; then
  RUN_ID=$(cloche get child_run_id 2>/dev/null || true)
  if [ -z "$RUN_ID" ]; then
    echo "warning: neither child_branch nor child_run_id found, skipping branch cleanup" >&2
    echo "Cleaned up run unknown"
    exit 0
  fi
  BRANCH="cloche/${RUN_ID}"
fi

BRANCH_SUFFIX="${BRANCH#cloche/}"
WORKTREE_DIR="$PROJECT_DIR/.gitworktrees/cloche/$BRANCH_SUFFIX"

if [ -d "$WORKTREE_DIR" ]; then
  git -C "$PROJECT_DIR" worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true
fi

git -C "$PROJECT_DIR" worktree prune 2>/dev/null || true

if git -C "$PROJECT_DIR" rev-parse --verify "$BRANCH" >/dev/null 2>&1; then
  git -C "$PROJECT_DIR" branch -D "$BRANCH" 2>/dev/null || true
fi

echo "Cleaned up $BRANCH"
