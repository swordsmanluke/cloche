#!/usr/bin/env bash
# vertical-pull-comments.sh — fetch PR comments for the address-feedback agent.
#
# Runs in-container. Reads:
#   current_pr_number — KV
# Writes:
#   feedback_path — KV; path to a markdown file containing all open comments
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/agent-creds.sh"

pr_number=$(clo get current_pr_number 2>/dev/null || true)
if [ -z "$pr_number" ]; then
  echo "error: current_pr_number not set in KV" >&2
  exit 1
fi

prompt_dir="$(clo get temp_file_dir 2>/dev/null || echo /workspace/.cloche/runs/${CLOCHE_RUN_ID})"
mkdir -p "$prompt_dir"
out="$prompt_dir/pr-feedback.md"

{
  echo "# Open feedback on PR #$pr_number"
  echo
  echo "## Review summaries"
  gh pr view "$pr_number" --json reviews \
    --jq '.reviews[] | select(.body != "" and .body != null) | "### \(.author.login) — \(.state)\n\n\(.body)\n"'
  echo
  echo "## Inline comments"
  gh api "repos/{owner}/{repo}/pulls/$pr_number/comments" \
    --jq '.[] | "### \(.user.login) on \(.path):\(.line // .original_line)\n\n> \(.body)\n"'
  echo
  echo "## Issue-level comments"
  gh pr view "$pr_number" --json comments \
    --jq '.comments[] | "### \(.author.login)\n\n\(.body)\n"'
} > "$out"

clo set feedback_path "$out"
echo "Wrote feedback context to $out"

# Make sure we're on the layer branch and have it up to date.
branch=$(clo get current_branch 2>/dev/null || true)
if [ -n "$branch" ]; then
  git fetch origin "$branch" 2>/dev/null || true
  git checkout "$branch" 2>/dev/null || git checkout -B "$branch" "origin/$branch"
  git pull --ff-only origin "$branch" 2>/dev/null || true
fi
