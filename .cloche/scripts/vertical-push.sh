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

# Write last_addressed_at at TWO scopes so the parent workflow's poll step
# can read it. `clo` (inside this sub-workflow container) defaults to writing
# at the child run's scope (taskID, parentAttempt, childRun). The parent's
# `cloche get` runs at (taskID, parentAttempt, parentRun) which doesn't match.
# We also write at attempt scope (runID="") so the daemon's KV-fallback finds
# it on the parent's first lookup miss.
now=$(date +%s)
clo set last_addressed_at "$now"
CLOCHE_RUN_ID="" clo set last_addressed_at "$now"

echo "Pushed feedback fixes to $branch (last_addressed_at=$now)"
