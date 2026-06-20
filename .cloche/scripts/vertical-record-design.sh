#!/usr/bin/env bash
# vertical-record-design.sh — stub: record that the design PR was approved.
#
# After this step, plan-feature proceeds with the design_branch recorded so
# prepare-test-plan-branch can base off it.
#
# TODO: L2 — replace this stub with real logic that:
#   1. Records design_branch in KV so subsequent steps can find it.
#   2. Clears PR-scoped KV keys (current_pr_number, current_branch,
#      last_addressed_at) to start the next phase clean.
set -euo pipefail
exec 2>&1
trap 'rc=$?; echo "FATAL: script failed at line $LINENO (rc=$rc): $BASH_COMMAND"; exit $rc' ERR

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

echo "stubRecordDesign: exiting 0 without writing to KV (L2 not yet implemented)"
echo "Feature: $feature_id"
