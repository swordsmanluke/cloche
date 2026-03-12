#!/usr/bin/env bash
# merge-to-main.sh — Rebase a cloche run branch onto main and fast-forward.
# Reads the child run ID from run context (stored by the executor via runcontext.Set).
# On conflict, prefers main's version. On failure, preserves the branch.
set -euo pipefail

RUN_ID=$(cloche get child_run_id)

if [ -z "$RUN_ID" ]; then
  echo "error: child_run_id not found in run context" >&2
  exit 1
fi

BRANCH="cloche/${RUN_ID}"
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"

export GIT_AUTHOR_NAME=cloche
export GIT_AUTHOR_EMAIL=cloche@local
export GIT_COMMITTER_NAME=cloche
export GIT_COMMITTER_EMAIL=cloche@local

# Verify the branch exists
if ! git -C "$PROJECT_DIR" rev-parse --verify "$BRANCH" >/dev/null 2>&1; then
  echo "error: branch $BRANCH does not exist" >&2
  exit 1
fi

WORKTREE_DIR="$PROJECT_DIR/.gitworktrees/merge/$RUN_ID"
mkdir -p "$(dirname "$WORKTREE_DIR")"

cleanup_worktree() {
  git -C "$PROJECT_DIR" worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true
}

# Create worktree at the feature branch
git -C "$PROJECT_DIR" worktree add "$WORKTREE_DIR" "$BRANCH"

# Rebase onto main — fail on conflict rather than silently dropping changes
if ! git -C "$WORKTREE_DIR" rebase main; then
  echo "error: rebase failed — branch $BRANCH preserved for review" >&2
  git -C "$WORKTREE_DIR" rebase --abort 2>/dev/null || true
  cleanup_worktree
  exit 1
fi

# Update the branch ref to the rebased result so main can fast-forward to it
REBASED_HEAD=$(git -C "$WORKTREE_DIR" rev-parse HEAD)

# Remove the worktree before merging (git won't allow branch deletion while
# a worktree is attached)
cleanup_worktree

# Update the branch ref to the rebased HEAD (worktree removal detaches it)
git -C "$PROJECT_DIR" update-ref "refs/heads/$BRANCH" "$REBASED_HEAD"

# Fast-forward main, updating the working tree
git -C "$PROJECT_DIR" merge --ff-only "$BRANCH"

# Delete the feature branch
git -C "$PROJECT_DIR" branch -D "$BRANCH" 2>/dev/null || echo "warning: could not delete branch $BRANCH" >&2

echo "Merged $BRANCH into main ($REBASED_HEAD)"
