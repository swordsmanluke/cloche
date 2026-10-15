#!/usr/bin/env bash
# claim.sh — Claim a GitHub issue by labelling it in-progress.
# Contract: kv task_id -> out task_title, task_body_path. A closed issue
# reports claim_status=unavailable and fails.
set -euo pipefail

label="${CLOCHE_GH_CLAIM_LABEL:-cloche:in-progress}"
TASK_ID="${CLOCHE_TASK_ID:-$(cloche get task_id)}"
if [ -z "$TASK_ID" ]; then
  echo "error: task_id not set" >&2
  exit 1
fi

issue=$(gh issue view "$TASK_ID" --json state,title,body) || {
  echo "error: could not look up issue $TASK_ID" >&2
  exit 1
}
if [ "$(echo "$issue" | jq -r '.state')" != "OPEN" ]; then
  cloche set claim_status unavailable
  echo "issue $TASK_ID is not open" >&2
  exit 1
fi

# Create the label on first use; ignore "already exists".
gh label create "$label" --color FBCA04 --description "Claimed by a cloche run" >/dev/null 2>&1 || true
gh issue edit "$TASK_ID" --add-label "$label" >/dev/null

body_path="$PWD/$(cloche get temp_file_dir)/task_body.md"
echo "$issue" | jq -r '.body // ""' > "$body_path"
cloche set task_title "$(echo "$issue" | jq -r '.title')"
cloche set task_body_path "$body_path"

echo "Claimed issue $TASK_ID"
