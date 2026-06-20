#!/usr/bin/env bash
# vertical-prepare-design-branch.sh — stub: create the design branch.
#
# TODO: L2 — replace this stub with real branch creation logic that:
#   1. Fetches and checks out a branch named vertical/<feature-id>/design
#      off the vertical_base_branch.
#   2. Records design_branch and current_branch in KV.
set -euo pipefail
exec 2>&1
trap 'rc=$?; echo "FATAL: script failed at line $LINENO (rc=$rc): $BASH_COMMAND"; exit $rc' ERR

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

echo "stubDesignBranchSetup: exiting 0 without creating branch (L2 not yet implemented)"
echo "Feature: $feature_id"
