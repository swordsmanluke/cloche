#!/usr/bin/env bash
# release.sh — Release a claimed issue by removing the in-progress label.
# Contract: kv task_id.
set -euo pipefail

label="${CLOCHE_GH_CLAIM_LABEL:-cloche:in-progress}"
TASK_ID="${CLOCHE_TASK_ID:-$(cloche get task_id 2>/dev/null || true)}"
if [ -z "$TASK_ID" ]; then
  echo "error: task_id not set" >&2
  exit 1
fi

gh issue edit "$TASK_ID" --remove-label "$label"
echo "Released issue $TASK_ID"
