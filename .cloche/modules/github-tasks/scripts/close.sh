#!/usr/bin/env bash
# close.sh — Close the GitHub issue after a successful run. Contract: kv task_id.
set -euo pipefail

label="${CLOCHE_GH_CLAIM_LABEL:-cloche:in-progress}"
TASK_ID="${CLOCHE_TASK_ID:-$(cloche get task_id 2>/dev/null || true)}"
if [ -z "$TASK_ID" ]; then
  echo "warning: task_id not set, skipping" >&2
  exit 0
fi

gh issue edit "$TASK_ID" --remove-label "$label" >/dev/null 2>&1 || true
gh issue close "$TASK_ID" >/dev/null 2>&1 && echo "Closed issue $TASK_ID" || echo "warning: could not close issue $TASK_ID" >&2
