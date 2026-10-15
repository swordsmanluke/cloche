#!/usr/bin/env bash
# release.sh — Release a stale claimed task back to open status.
# Contract: kv task_id.
set -euo pipefail

TASK_ID="${CLOCHE_TASK_ID:-$(cloche get task_id 2>/dev/null || true)}"

if [ -z "$TASK_ID" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

# Return the ticket to open status and unassign the owner.
bd update "$TASK_ID" -s open 2>&1
echo "Released task $TASK_ID"
