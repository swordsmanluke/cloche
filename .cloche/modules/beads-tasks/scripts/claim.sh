#!/usr/bin/env bash
# claim.sh — Claim the daemon-assigned task by setting it to in_progress.
# Contract: kv task_id -> out task_title, task_body_path. A task that is no
# longer open (closed) reports claim_status=unavailable and fails.
set -euo pipefail

TASK_ID="${CLOCHE_TASK_ID:-$(cloche get task_id)}"
if [ -z "$TASK_ID" ]; then
  echo "error: task_id not set" >&2
  exit 1
fi

task_json=$(bd show "$TASK_ID" --json 2>/dev/null) || {
  echo "error: could not look up task $TASK_ID" >&2
  exit 1
}
if [ "$(echo "$task_json" | jq -r '.[0].status // empty')" = "closed" ]; then
  cloche set claim_status unavailable
  echo "task $TASK_ID is closed" >&2
  exit 1
fi

# Force status to in_progress regardless of prior state. This avoids the
# "already claimed" error when a previous run left the assignee set but the
# status was reset to open.
bd update "$TASK_ID" -s in_progress 2>&1

title=$(echo "$task_json" | jq -r '.[0].title // empty')
body_path="$PWD/$(cloche get temp_file_dir)/task_body.md"
echo "$task_json" | jq -r '.[0].description // empty' > "$body_path"
cloche set task_title "$title"
cloche set task_body_path "$body_path"

echo "Claimed task $TASK_ID"
