#!/usr/bin/env bash
# close-task.sh — Close or release the task after main completes.
# Runs in the finalize phase. Uses CLOCHE_TASK_ID env var and
# CLOCHE_MAIN_OUTCOME to decide whether to close or release.
set -euo pipefail

TASK_ID="${CLOCHE_TASK_ID:-}"
OUTCOME="${CLOCHE_MAIN_OUTCOME:-failed}"
RUN_ID="${CLOCHE_MAIN_RUN_ID:-unknown}"

if [ -z "$TASK_ID" ]; then
  echo "warning: CLOCHE_TASK_ID not set, skipping" >&2
  exit 0
fi

if [ "$OUTCOME" = "succeeded" ]; then
  bd close "$TASK_ID" 2>/dev/null && echo "Closed task $TASK_ID" || echo "warning: could not close task $TASK_ID" >&2
else
  # Log the failure on the ticket so the next worker (or a human) has context.
  bd comments add "$TASK_ID" "Run $RUN_ID failed (outcome: $OUTCOME). Releasing task for retry. Use \`cloche logs $RUN_ID\` to inspect." 2>/dev/null || true

  # Release the task back to open so the orchestration loop can reassign it.
  bd update "$TASK_ID" -s open 2>/dev/null && echo "Released task $TASK_ID for retry" || echo "warning: could not release task $TASK_ID" >&2
fi
