#!/usr/bin/env bash
# close.sh — Close the task after a successful run.
# Contract: kv task_id.
set -euo pipefail

TASK_ID="${CLOCHE_TASK_ID:-$(cloche get task_id 2>/dev/null || true)}"

if [ -z "$TASK_ID" ]; then
  echo "warning: CLOCHE_TASK_ID not set, skipping" >&2
  exit 0
fi

bd close "$TASK_ID" 2>/dev/null && echo "Closed task $TASK_ID" || echo "warning: could not close task $TASK_ID" >&2
