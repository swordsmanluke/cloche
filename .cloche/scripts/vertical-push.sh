#!/usr/bin/env bash
# vertical-push.sh — push fixes from address-pr-feedback back up to the PR branch.
#
# Runs in-container. Records last_addressed_at so poll-pr knows which comments
# pre-date this round of fixes.
set -euo pipefail

branch=$(clo get current_branch 2>/dev/null || true)
if [ -z "$branch" ]; then
  echo "error: current_branch not set in KV" >&2
  exit 1
fi

# Ensure something was committed before we push.
if [ -z "$(git log --oneline "origin/$branch..$branch" 2>/dev/null)" ]; then
  echo "warning: no new commits on $branch — pushing anyway in case the index moved" >&2
fi

git push origin "$branch"
clo set last_addressed_at "$(date +%s)"
echo "Pushed feedback fixes to $branch"
