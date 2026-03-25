#!/usr/bin/env bash
# close-task.sh — Close the task after a successful run.
# This step is wired on the success path (merge:success → bump-version → close-task),
# so reaching it means the work succeeded.
set -euo pipefail

TASK_ID="${CLOCHE_TASK_ID:-}"

if [ -z "$TASK_ID" ]; then
  echo "warning: CLOCHE_TASK_ID not set, skipping" >&2
  exit 0
fi

bd close "$TASK_ID" 2>/dev/null && echo "Closed task $TASK_ID" || echo "warning: could not close task $TASK_ID" >&2
