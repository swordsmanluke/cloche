#!/usr/bin/env bash
# vertical-poll-pr.sh — poll script for the current layer's PR.
#
# Source agent credentials so `gh pr view` queries via the bot identity rather
# than the developer's. (The script only reads — no writes — but consistency
# avoids mixed identity surprises.)
#
# Decision rules:
#   approved  — at least one APPROVED review, no outstanding CHANGES_REQUESTED
#   feedback  — any CHANGES_REQUESTED review, or any review / inline review
#               comment / issue comment newer than last_addressed_at
#   fail      — PR is closed without merging
#   pending   — anything else (exit 0 with no marker)
#
# "Feedback" includes line-level review comments (`/pulls/N/comments`), which is
# what GitHub's "Comment" review type emits and is the natural way reviewers
# leave PR feedback. A review with state COMMENTED counts even if its body is
# empty, because the comments live on the review's child threads.
#
# Reads:
#   current_pr_number — KV
#   last_addressed_at — KV (unix epoch); set by address-pr-feedback after pushing fixes
set -uo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/agent-creds.sh" 2>/dev/null || true

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

# Any feedback newer than the last time we addressed it also triggers feedback.
# Three sources count:
#   1. Issue-level PR comments (from `gh pr view --json comments`)
#   2. Reviews — every non-APPROVED review (CHANGES_REQUESTED was handled above,
#      and a COMMENTED review wraps inline line-level comments even if its body
#      is empty)
#   3. Inline line-level review comments (`/pulls/N/comments` REST endpoint),
#      which is where "Comment" reviews put their per-line notes
inline_json=$(gh api "repos/{owner}/{repo}/pulls/$pr_number/comments" 2>/dev/null) || inline_json="[]"

newest_comment_ts=$(jq -n \
  --argjson pr "$pr_json" \
  --argjson inline "$inline_json" '
  ([$pr.comments[]?.createdAt]
   + [$pr.reviews[]? | select(.state != "APPROVED") | .submittedAt]
   + [$inline[]?.created_at])
  | map(fromdateiso8601? // 0)
  | max // 0
')

total_feedback=$(jq -n \
  --argjson pr "$pr_json" \
  --argjson inline "$inline_json" '
  ($pr.comments | length)
  + ([$pr.reviews[]? | select(.state != "APPROVED")] | length)
  + ($inline | length)
')

if [ "$newest_comment_ts" -gt "$last_addressed" ] && [ "$total_feedback" -gt 0 ]; then
  echo "PR #$pr_number has new feedback since last address ($total_feedback item(s))."
  echo "CLOCHE_RESULT:feedback"
  exit 0
fi

if [ "$approvals" -gt 0 ]; then
  echo "PR #$pr_number is approved."
  echo "CLOCHE_RESULT:approved"
  exit 0
fi

# Pending — exit 0 with no marker so the orchestrator polls again.
exit 0
