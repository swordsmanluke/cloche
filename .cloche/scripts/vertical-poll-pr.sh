#!/usr/bin/env bash
# vertical-poll-pr.sh — poll script for the current layer's PR.
#
# Decision rules:
#   approved  — at least one APPROVED review, no outstanding CHANGES_REQUESTED
#   feedback  — any CHANGES_REQUESTED review, or unresolved comments newer than
#               last_addressed_at
#   fail      — PR is closed without merging
#   pending   — anything else (exit 0 with no marker)
#
# Reads:
#   current_pr_number — KV
#   last_addressed_at — KV (unix epoch); set by address-pr-feedback after pushing fixes
set -uo pipefail

pr_number=$(cloche get current_pr_number 2>/dev/null || true)
if [ -z "$pr_number" ]; then
  echo "error: current_pr_number not set in KV" >&2
  echo "CLOCHE_RESULT:fail"
  exit 0
fi

last_addressed=$(cloche get last_addressed_at 2>/dev/null || echo "0")

pr_json=$(gh pr view "$pr_number" --json state,reviews,comments,mergeable 2>/dev/null) || {
  echo "warning: gh pr view failed for #$pr_number, will retry" >&2
  exit 0
}

state=$(echo "$pr_json" | jq -r '.state')

if [ "$state" = "CLOSED" ]; then
  echo "PR #$pr_number was closed without merging."
  echo "CLOCHE_RESULT:fail"
  exit 0
fi

# Take only the latest review per author for the approval calculus.
latest_reviews=$(echo "$pr_json" | jq -c '
  .reviews
  | sort_by(.submittedAt)
  | group_by(.author.login)
  | map(.[-1])
')

approvals=$(echo "$latest_reviews" | jq '[.[] | select(.state == "APPROVED")] | length')
changes_req=$(echo "$latest_reviews" | jq '[.[] | select(.state == "CHANGES_REQUESTED")] | length')

if [ "$changes_req" -gt 0 ]; then
  echo "PR #$pr_number has CHANGES_REQUESTED — addressing feedback."
  echo "CLOCHE_RESULT:feedback"
  exit 0
fi

# Any comment newer than the last time we addressed feedback also triggers feedback.
newest_comment_ts=$(echo "$pr_json" | jq -r '
  [.comments[]?.createdAt] + [.reviews[]? | select(.body != "" and .body != null) | .submittedAt]
  | map(fromdateiso8601? // 0)
  | max // 0
')

if [ "$newest_comment_ts" -gt "$last_addressed" ]; then
  # Only treat as feedback if at least one comment exists; if the PR has zero comments
  # the timestamp will be 0 which can't exceed last_addressed (also 0).
  total_comments=$(echo "$pr_json" | jq '(.comments | length) + ([.reviews[]? | select(.body != "" and .body != null)] | length)')
  if [ "$total_comments" -gt 0 ]; then
    echo "PR #$pr_number has new comments since last address."
    echo "CLOCHE_RESULT:feedback"
    exit 0
  fi
fi

if [ "$approvals" -gt 0 ]; then
  echo "PR #$pr_number is approved."
  echo "CLOCHE_RESULT:approved"
  exit 0
fi

# Pending — exit 0 with no marker so the orchestrator polls again.
exit 0
