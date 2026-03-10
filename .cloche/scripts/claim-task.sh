#!/usr/bin/env bash
# claim-task.sh — Claim a bead task by setting it to in_progress.
# Expects CLOCHE_TASK_ID env var from the previous step's output mapping.
set -euo pipefail

if [ -z "${CLOCHE_TASK_ID:-}" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

bd update "$CLOCHE_TASK_ID" --claim >&2

# Forward task info as JSON so downstream steps can access it via output mappings.
cat <<EOF
[{"id":"${CLOCHE_TASK_ID}","title":"${CLOCHE_TASK_TITLE:-}","description":"${CLOCHE_TASK_BODY:-}"}]
EOF
