#!/usr/bin/env bash
# vertical-open-design-pr.sh — stub: push the design branch and open a PR.
#
# TODO: L2 — replace this stub with real logic that:
#   1. Pushes the design branch to origin.
#   2. Opens a GitHub PR and records current_pr_number in KV.
set -euo pipefail
exec 2>&1
trap 'rc=$?; echo "FATAL: script failed at line $LINENO (rc=$rc): $BASH_COMMAND"; exit $rc' ERR

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

echo "stubOpenDesignPR: exiting 0 without pushing or opening a real PR (L2 not yet implemented)"
echo "Feature: $feature_id"
