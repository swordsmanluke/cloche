#!/usr/bin/env bash
# claim-task.sh — Claim the daemon-assigned task by setting it to in_progress.
# Uses CLOCHE_TASK_ID env var set by the daemon's three-phase loop.
set -euo pipefail

if [ -z "${CLOCHE_TASK_ID:-}" ]; then
  echo "error: CLOCHE_TASK_ID not set (is the daemon running in three-phase mode?)" >&2
  exit 1
fi

# Force status to in_progress and assign to us, regardless of prior state.
# This avoids the "already claimed" error when a previous run left the
# assignee set but the status was reset to open.
bd update "$CLOCHE_TASK_ID" -s in_progress 2>&1

echo "Claimed task $CLOCHE_TASK_ID"
