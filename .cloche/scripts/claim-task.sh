#!/usr/bin/env bash
# claim-task.sh — Claim a bead task by setting it to in_progress.
# Reads task data from run context (set by ready-tasks.sh via cloche set).
set -euo pipefail

CLOCHE_TASK_ID=$(cloche get task_id)
CLOCHE_TASK_TITLE=$(cloche get task_title)
CLOCHE_TASK_BODY=$(cloche get task_body)

if [ -z "$CLOCHE_TASK_ID" ]; then
  echo "error: task_id not found in run context" >&2
  exit 1
fi

# Capture bd output separately so it doesn't corrupt our JSON stdout.
claim_output=$(bd update "$CLOCHE_TASK_ID" --claim 2>&1) || true

# bd exits 0 even on failure, so check for error in output.
if echo "$claim_output" | grep -qi "error\|already claimed"; then
  echo "claim failed: $claim_output" >&2
  exit 1
fi

# Forward task info as properly escaped JSON so downstream steps can parse it.
jq -n --arg id "$CLOCHE_TASK_ID" \
      --arg title "$CLOCHE_TASK_TITLE" \
      --arg desc "$CLOCHE_TASK_BODY" \
      '[{id: $id, title: $title, description: $desc}]'
