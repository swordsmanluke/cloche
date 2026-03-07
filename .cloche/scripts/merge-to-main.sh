#!/usr/bin/env bash
# merge-to-main.sh — Rebase a cloche run branch onto main and fast-forward.
# Expects CLOCHE_PREV_OUTPUT to point to a file containing the run ID.
# On conflict, prefers main's version. On failure, preserves the branch.
set -euo pipefail

if [ -z "${CLOCHE_PREV_OUTPUT:-}" ] || [ ! -f "$CLOCHE_PREV_OUTPUT" ]; then
  echo "error: CLOCHE_PREV_OUTPUT not set or file missing" >&2
  exit 1
fi

RUN_ID=$(cat "$CLOCHE_PREV_OUTPUT" | tr -d '[:space:]')
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

# Rebase onto main, preferring main on conflict
if ! git -C "$WORKTREE_DIR" rebase -X ours main; then
  echo "error: rebase failed — branch $BRANCH preserved for review" >&2
  git -C "$WORKTREE_DIR" rebase --abort 2>/dev/null || true
  cleanup_worktree
  exit 1
fi

# Read rebased HEAD before removing worktree
REBASED_HEAD=$(git -C "$WORKTREE_DIR" rev-parse HEAD)

# Must remove worktree before updating refs
cleanup_worktree

# Fast-forward main to rebased HEAD
git -C "$PROJECT_DIR" update-ref refs/heads/main "$REBASED_HEAD"

# Delete the feature branch
git -C "$PROJECT_DIR" branch -D "$BRANCH" 2>/dev/null || echo "warning: could not delete branch $BRANCH" >&2

echo "Merged $BRANCH into main ($REBASED_HEAD)"
