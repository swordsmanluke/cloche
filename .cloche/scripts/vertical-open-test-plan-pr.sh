#!/usr/bin/env bash
# vertical-open-test-plan-pr.sh — open the PR for the BDD test plan.
#
# Reads:
#   vertical_base_branch — KV (default "main")
# Writes:
#   current_pr_number — KV
#   current_branch    — KV
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/agent-creds.sh"
source "$(dirname "${BASH_SOURCE[0]}")/lib/vertical-extract.sh"

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

base=$(cloche get vertical_base_branch 2>/dev/null || echo "main")
branch="vertical/${feature_id}/test-plan"
cloche set current_branch "$branch"

rename_extracted_to "$branch"
git push -u origin "$branch"

feature_title=$(bd show "$feature_id" --json 2>/dev/null | jq -r '.[0].title // empty')
[ -z "$feature_title" ] && feature_title="Feature $feature_id"

body_file="$(cloche get temp_file_dir)/test-plan-pr-body.md"
agent_desc=""
temp_file_dir=$(cloche get temp_file_dir 2>/dev/null || true)
if [ -n "$temp_file_dir" ] && [ -f "$temp_file_dir/pr-description.md" ]; then
  agent_desc=$(cat "$temp_file_dir/pr-description.md")
fi

if [ -n "$agent_desc" ]; then
  printf '%s\n\n' "$agent_desc" > "$body_file"
else
  cat > "$body_file" <<EOF
## BDD test plan for: $feature_title

First PR in the vertical-development stack for \`$feature_id\`. Contains only
Gherkin \`.feature\` files and pending step stubs — no implementation. Each
subsequent layer PR makes a subset of these scenarios pass.

EOF
fi

cat >> "$body_file" <<EOF
---
**Feature:** \`$feature_id\` · **Base:** \`$base\`

Approve if the scenarios capture the feature you wanted. Leave comments to
rewrite/add/remove scenarios — \`address-test-plan-feedback\` picks comments up on
the next 60s poll.
EOF

existing=$(gh pr list --head "$branch" --json number --jq '.[0].number' 2>/dev/null || true)
if [ -n "$existing" ]; then
  gh pr edit "$existing" --title "[test-plan] $feature_title" --body-file "$body_file" >/dev/null
  pr_number="$existing"
  echo "Updated existing test-plan PR #$pr_number"
else
  pr_url=$(gh pr create --base "$base" --head "$branch" \
    --title "[test-plan] $feature_title" --body-file "$body_file")
  pr_number=$(echo "$pr_url" | grep -oE '[0-9]+$')
  echo "Opened test-plan PR #$pr_number → $pr_url"
fi

cloche set current_pr_number "$pr_number"
cloche set last_addressed_at "0"
