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
cat > "$body_file" <<EOF
## BDD test plan for: $feature_title

This is the **first PR** in a vertical-development run for $feature_id. It contains
only Gherkin \`.feature\` files and stub step definitions — no implementation.

The scenarios describe the feature's expected user-facing behavior. They will all
fail (or be marked pending) right now, by design. Each subsequent layer PR in this
stack should make a subset of them pass.

**Approve** if the scenarios capture the feature's intent correctly, then the
implementation layers will begin.

**Request changes** to add, remove, or rewrite scenarios — the workflow will
re-run an agent against your feedback and push updates.
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
