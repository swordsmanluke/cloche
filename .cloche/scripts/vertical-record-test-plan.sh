#!/usr/bin/env bash
# vertical-record-test-plan.sh — record that the test plan PR was approved.
#
# After this step, `pick-next-layer` will use the test-plan branch as the base
# for L1 (instead of the user-specified vertical_base_branch).
set -euo pipefail

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

cloche set test_plan_branch "vertical/${feature_id}/test-plan"

# Clear PR-scoped KV so the next phase starts clean.
cloche set current_pr_number ""
cloche set current_branch ""
cloche set last_addressed_at ""

echo "Test plan branch recorded; ready to begin layers."
