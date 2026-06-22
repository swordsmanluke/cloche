#!/usr/bin/env bash
# vertical-open-design-pr.sh — push the design branch and open a PR.
#
# The PR body includes the "Open Questions for Reviewer" section from the design
# doc verbatim so reviewers see what to answer without opening the file.
#
# Reads (KV):
#   vertical_base_branch — PR target base (default "main")
#   design_doc_path      — path to the written design doc (set by write-design agent)
# Writes (KV):
#   current_pr_number, last_addressed_at
set -euo pipefail
exec 2>&1
trap 'rc=$?; echo "FATAL: script failed at line $LINENO (rc=$rc): $BASH_COMMAND"; exit $rc' ERR
source "$(dirname "${BASH_SOURCE[0]}")/lib/agent-creds.sh"
source "$(dirname "${BASH_SOURCE[0]}")/lib/vertical-extract.sh"

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

base=$(cloche get vertical_base_branch 2>/dev/null || echo "main")
branch="vertical/${feature_id}/design"
cloche set current_branch "$branch"

rename_extracted_to "$branch"
git push -u origin "$branch"

feature_title=$(bd show "$feature_id" --json 2>/dev/null | jq -r '.[0].title // empty')
[ -z "$feature_title" ] && feature_title="Feature $feature_id"

# Extract Open Questions section from the design doc.
open_questions=""
doc_path=$(cloche get design_doc_path 2>/dev/null || true)
if [ -n "$doc_path" ] && [ -f "$doc_path" ]; then
  open_questions=$(awk '/^## Open Questions for Reviewer/{found=1; next} found && /^## /{exit} found{print}' "$doc_path")
fi

temp_file_dir=$(cloche get temp_file_dir 2>/dev/null || true)
body_file="${temp_file_dir:-.}/design-pr-body.md"

agent_desc=""
if [ -n "$temp_file_dir" ] && [ -f "$temp_file_dir/pr-description.md" ]; then
  agent_desc=$(cat "$temp_file_dir/pr-description.md")
fi

if [ -n "$agent_desc" ]; then
  printf '%s\n\n' "$agent_desc" > "$body_file"
else
  cat > "$body_file" <<EOF
## Design doc for: $feature_title

Design document for \`$feature_id\`. Approve when the approach looks correct.
Leave PR comments to request changes — the workflow will address them automatically.

EOF
fi

if [ -n "$open_questions" ]; then
  printf '## Open Questions for Reviewer\n\n%s\n\n' "$open_questions" >> "$body_file"
fi

cat >> "$body_file" <<EOF
---
**Feature:** \`$feature_id\` · **Base:** \`$base\`
EOF

existing=$(gh pr list --head "$branch" --json number --jq '.[0].number' 2>/dev/null || true)
if [ -n "$existing" ]; then
  gh pr edit "$existing" --title "[design] $feature_title" --body-file "$body_file" >/dev/null
  pr_number="$existing"
  echo "Updated existing design PR #$pr_number"
else
  pr_url=$(gh pr create --base "$base" --head "$branch" \
    --title "[design] $feature_title" --body-file "$body_file")
  pr_number=$(echo "$pr_url" | grep -oE '[0-9]+$')
  echo "Opened design PR #$pr_number → $pr_url"
fi

cloche set current_pr_number "$pr_number"
cloche set last_addressed_at "0"
