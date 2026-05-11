#!/usr/bin/env bash
# vertical-open-docs-pr.sh — open the PR for the documentation update.
#
# The docs branch is based on the bottom-most layer's branch, so the docs PR
# targets that branch. The PR diff shows only the docs changes (the layer code
# is already in the base branch as part of the stack).
#
# Reads (KV):
#   nothing required — branch and base derived from feature ID and bead
# Writes (KV):
#   current_pr_number, current_branch
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/agent-creds.sh"
source "$(dirname "${BASH_SOURCE[0]}")/lib/vertical-extract.sh"

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

# Bottom-most layer's branch is our base.
layers_json=$(bd list --parent "$feature_id" --all --json 2>/dev/null) || layers_json="[]"
last_layer=$(echo "$layers_json" | jq -r '[.[] | select(.status == "closed")] | last.id // empty')
if [ -z "$last_layer" ]; then
  echo "error: no closed layers for feature $feature_id" >&2
  exit 1
fi

base="vertical/${feature_id}/${last_layer}"
branch="vertical/${feature_id}/docs"
cloche set current_branch "$branch"

rename_extracted_to "$branch"
git push -u origin "$branch"

feature_title=$(bd show "$feature_id" --json 2>/dev/null | jq -r '.[0].title // empty')
[ -z "$feature_title" ] && feature_title="Feature $feature_id"

body_file="$(cloche get temp_file_dir)/docs-pr-body.md"
cat > "$body_file" <<EOF
## Documentation update for: $feature_title

Final PR in the vertical stack for $feature_id. Updates project documentation to
reflect the new feature.

Inline code comments were updated during layer implementation; this PR covers
project-level docs only (\`docs/\`, \`README.md\`, \`CHANGELOG.md\`, etc.).

**Approve** to trigger \`finalize\`, which squash-merges the entire stack
(test plan + all layers + this docs PR) into the user-specified base branch as a
single commit.
EOF

existing=$(gh pr list --head "$branch" --json number --jq '.[0].number' 2>/dev/null || true)
if [ -n "$existing" ]; then
  gh pr edit "$existing" --title "[docs] $feature_title" --body-file "$body_file" >/dev/null
  pr_number="$existing"
  echo "Updated existing docs PR #$pr_number"
else
  pr_url=$(gh pr create --base "$base" --head "$branch" \
    --title "[docs] $feature_title" --body-file "$body_file")
  pr_number=$(echo "$pr_url" | grep -oE '[0-9]+$')
  echo "Opened docs PR #$pr_number → $pr_url"
fi

cloche set current_pr_number "$pr_number"
cloche set last_addressed_at "0"
