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

# Auto-commit any uncommitted changes left by the respond step (e.g. if the
# agent's Bash tool was unavailable during the respond phase).
if ! git diff --quiet HEAD 2>/dev/null || ! git diff --cached --quiet 2>/dev/null; then
  echo "auto-committing uncommitted changes from respond step..."
  git add -A
  git commit -m "address-feedback: auto-committed by push step"
fi

# Ensure something was committed before we push.
if [ -z "$(git log --oneline "origin/$branch..$branch" 2>/dev/null)" ]; then
  echo "warning: no new commits on $branch — pushing anyway in case the index moved" >&2
fi

git push origin "$branch"

# Post any pending PR comment replies left by the respond step.
prompt_dir="$(clo get temp_file_dir 2>/dev/null || echo /workspace/.cloche/runs/${CLOCHE_RUN_ID:-unknown})"
reply_file="$prompt_dir/pending-reply.txt"
reply_id_file="$prompt_dir/pending-reply-comment-id.txt"
if [ -f "$reply_file" ] && [ -f "$reply_id_file" ]; then
  comment_id=$(cat "$reply_id_file")
  pr_number=$(clo get current_pr_number 2>/dev/null || true)
  if [ -n "$comment_id" ] && [ -n "$pr_number" ]; then
    gh api "repos/{owner}/{repo}/pulls/$pr_number/comments/$comment_id/replies" \
      -X POST -f body="$(cat "$reply_file")" \
      && echo "Posted reply to comment $comment_id on PR #$pr_number" \
      || echo "warning: failed to post reply to comment $comment_id" >&2
    rm -f "$reply_file" "$reply_id_file"
  fi
fi

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
