#!/usr/bin/env bash
# close-task.sh — Close the task after main completes.
# Runs in the finalize phase. Uses CLOCHE_TASK_ID env var and
# CLOCHE_MAIN_OUTCOME to decide whether to close or reopen.
set -euo pipefail

TASK_ID="${CLOCHE_TASK_ID:-}"
OUTCOME="${CLOCHE_MAIN_OUTCOME:-failed}"

if [ -z "$TASK_ID" ]; then
  echo "warning: CLOCHE_TASK_ID not set, skipping" >&2
  exit 0
fi

if [ "$OUTCOME" = "succeeded" ]; then
  bd close "$TASK_ID" 2>/dev/null && echo "Closed task $TASK_ID" || echo "warning: could not close task $TASK_ID" >&2
else
  echo "Main outcome was $OUTCOME — task $TASK_ID left open for retry"
fi
