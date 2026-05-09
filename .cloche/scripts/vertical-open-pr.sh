#!/usr/bin/env bash
# vertical-open-pr.sh — open (or update) a PR for the current layer's branch.
#
# Reads:
#   current_layer_id     — KV
#   current_base_branch  — KV (previous layer's branch or `main`)
#   implement_status     — KV ("success" or "stuck")
# Writes:
#   current_pr_number    — KV
#   current_branch       — KV (layer branch name)
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/agent-creds.sh"

feature_id="${CLOCHE_TASK_ID:-}"
layer_id=$(cloche get current_layer_id 2>/dev/null || true)
base=$(cloche get current_base_branch 2>/dev/null || echo "main")
status=$(cloche get implement_status 2>/dev/null || echo "success")

if [ -z "$layer_id" ]; then
  echo "error: current_layer_id not set in KV" >&2
  exit 1
fi

branch="vertical/${feature_id}/${layer_id}"
cloche set current_branch "$branch"

# Push the branch so gh pr create can see it.
git push -u origin "$branch"

# Build PR title and body.
layer_title=$(bd show "$layer_id" --json 2>/dev/null | jq -r '.[0].title // empty')
if [ -z "$layer_title" ]; then
  layer_title="Layer $layer_id"
fi

if [ "$status" = "stuck" ]; then
  title="[stuck] $layer_title"
  body_file="$(cloche get temp_file_dir)/pr-body.md"
  cat > "$body_file" <<EOF
## Status: needs help

The vertical workflow's implement step gave up on this layer. The branch contains
whatever partial work was completed before giving up.

**Layer task:** $layer_id
**Parent feature:** $feature_id
**Base branch:** $base

Review the diff and leave guidance as PR comments — the workflow will pick those up
on the next poll, run the address-pr-feedback sub-workflow against them, and try
again. Approve the PR once the layer is in good shape.

EOF
  if [ -f ".cloche/runs/${CLOCHE_RUN_ID}/agent-give-up-reason.md" ]; then
    echo "## Agent's last note" >> "$body_file"
    echo >> "$body_file"
    cat ".cloche/runs/${CLOCHE_RUN_ID}/agent-give-up-reason.md" >> "$body_file"
  fi
else
  title="$layer_title"
  body_file="$(cloche get temp_file_dir)/pr-body.md"
  cat > "$body_file" <<EOF
## Layer ready for review

**Layer task:** $layer_id
**Parent feature:** $feature_id
**Base branch:** $base

This PR adds one vertical slice of the parent feature. Anything below this layer is
mocked — those mocks will be replaced by the next layer's PR.

Approve to unblock the next layer. Leave comments to request changes.
EOF
fi

# If a PR already exists for this branch (re-run), update it instead of creating.
existing=$(gh pr list --head "$branch" --json number --jq '.[0].number' 2>/dev/null || true)
if [ -n "$existing" ]; then
  gh pr edit "$existing" --title "$title" --body-file "$body_file" >/dev/null
  pr_number="$existing"
  echo "Updated existing PR #$pr_number"
else
  pr_url=$(gh pr create --base "$base" --head "$branch" --title "$title" --body-file "$body_file")
  pr_number=$(echo "$pr_url" | grep -oE '[0-9]+$')
  echo "Opened PR #$pr_number → $pr_url"
fi

cloche set current_pr_number "$pr_number"
cloche set last_addressed_at "0"
