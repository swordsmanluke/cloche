#!/usr/bin/env bash
# vertical-record-design.sh — record that the design PR was approved.
#
# Sets design_branch in KV so prepare-test-plan-branch and finalize can find it.
# Clears PR-scoped KV keys to start the next phase clean.
set -euo pipefail
exec 2>&1
trap 'rc=$?; echo "FATAL: script failed at line $LINENO (rc=$rc): $BASH_COMMAND"; exit $rc' ERR

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

# Authoritative record that Phase 0.5 completed and the design was approved.
design_branch="vertical/${feature_id}/design"
cloche set design_branch "$design_branch"

# Clear PR-scoped KV so the next phase starts clean.
cloche set current_pr_number ""
cloche set current_branch ""
cloche set last_addressed_at ""

echo "Design branch recorded: $design_branch. Ready for plan-feature."
