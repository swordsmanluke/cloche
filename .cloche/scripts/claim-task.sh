#!/usr/bin/env bash
# claim-task.sh — Claim the daemon-assigned task by setting it to in_progress.
# Uses CLOCHE_TASK_ID env var set by the daemon's three-phase loop.
set -euo pipefail

if [ -z "${CLOCHE_TASK_ID:-}" ]; then
  echo "error: CLOCHE_TASK_ID not set (is the daemon running in three-phase mode?)" >&2
  exit 1
fi

# Claim the task in bead
claim_output=$(bd update "$CLOCHE_TASK_ID" --claim 2>&1) || true

if echo "$claim_output" | grep -qi "error\|already claimed"; then
  echo "claim failed: $claim_output" >&2
  exit 1
fi

echo "Claimed task $CLOCHE_TASK_ID"
