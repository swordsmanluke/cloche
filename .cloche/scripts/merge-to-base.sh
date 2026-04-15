#!/usr/bin/env bash
# merge-to-base.sh — Rebase a cloche extract branch onto the current base branch
# and fast-forward. The branch and its worktree are pre-created by the daemon
# (shared across sub-workflows that reuse the same container.id within an
# attempt); the branch name lives in KV as child_branch.
# On conflict, preserves the branch for review. On failure, exits non-zero.
set -euo pipefail

BRANCH=$(cloche get child_branch 2>/dev/null || true)
if [ -z "$BRANCH" ]; then
  RUN_ID=$(cloche get child_run_id 2>/dev/null || true)
  if [ -z "$RUN_ID" ]; then
    echo "error: neither child_branch nor child_run_id found in run context" >&2
    exit 1
  fi
  BRANCH="cloche/${RUN_ID}"
fi

# Worktree lives at .gitworktrees/cloche/<branch-suffix>, where <branch-suffix>
# is the portion of the branch name after the leading "cloche/".
BRANCH_SUFFIX="${BRANCH#cloche/}"
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
WORKTREE_DIR="$PROJECT_DIR/.gitworktrees/cloche/$BRANCH_SUFFIX"
BASE_BRANCH=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD)

export GIT_AUTHOR_NAME=cloche
export GIT_AUTHOR_EMAIL=cloche@local
export GIT_COMMITTER_NAME=cloche
export GIT_COMMITTER_EMAIL=cloche@local

if ! git -C "$PROJECT_DIR" rev-parse --verify "$BRANCH" >/dev/null 2>&1; then
  echo "error: branch $BRANCH does not exist" >&2
  exit 1
fi

if [ ! -d "$WORKTREE_DIR" ]; then
  echo "error: worktree $WORKTREE_DIR does not exist" >&2
  exit 1
fi

# Stash untracked files in the main working tree so they don't conflict with
# the incoming branch during the fast-forward merge.
STASH_CREATED=0
if ! git -C "$PROJECT_DIR" stash --include-untracked -m "cloche/merge-to-base: $BRANCH" 2>/dev/null; then
  echo "warning: could not stash untracked files — proceeding anyway" >&2
else
  if git -C "$PROJECT_DIR" stash list | grep -q "cloche/merge-to-base: $BRANCH"; then
    STASH_CREATED=1
  fi
fi

# Rebase onto base branch inside the pre-created worktree — fail on conflict
# rather than silently dropping changes.
if ! git -C "$WORKTREE_DIR" rebase "$BASE_BRANCH"; then
  echo "error: rebase failed — branch $BRANCH preserved for review" >&2
  git -C "$WORKTREE_DIR" rebase --abort 2>/dev/null || true
  if [ "$STASH_CREATED" -eq 1 ]; then
    git -C "$PROJECT_DIR" stash pop || true
  fi
  exit 1
fi

REBASED_HEAD=$(git -C "$WORKTREE_DIR" rev-parse HEAD)

# Remove the worktree before merging (git won't allow branch deletion while
# a worktree is attached).
git -C "$PROJECT_DIR" worktree remove --force "$WORKTREE_DIR" 2>/dev/null || true

# Update the branch ref to the rebased HEAD (worktree removal detaches it).
git -C "$PROJECT_DIR" update-ref "refs/heads/$BRANCH" "$REBASED_HEAD"

# Fast-forward base branch, updating the working tree.
git -C "$PROJECT_DIR" merge --ff-only "$BRANCH"

# Restore stashed untracked files.
if [ "$STASH_CREATED" -eq 1 ]; then
  git -C "$PROJECT_DIR" stash pop || echo "warning: could not restore stashed files" >&2
fi

# Delete the feature branch.
git -C "$PROJECT_DIR" branch -D "$BRANCH" 2>/dev/null || echo "warning: could not delete branch $BRANCH" >&2

echo "Merged $BRANCH into $BASE_BRANCH ($REBASED_HEAD)"
