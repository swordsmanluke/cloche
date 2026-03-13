#!/usr/bin/env bash
# release-task.sh — Release a stale claimed task back to open status.
# Uses CLOCHE_TASK_ID env var set by the caller.
set -euo pipefail

TASK_ID="${CLOCHE_TASK_ID:-}"

if [ -z "$TASK_ID" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

# Return the ticket to open status and unassign the owner.
bd update "$TASK_ID" -s open 2>&1
echo "Released task $TASK_ID"
