#!/usr/bin/env bash
# vertical-prepare-test-plan-branch.sh — create the test-plan branch off the
# user-specified base.
#
# Runs in-container.
set -euo pipefail

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

base=$(clo get vertical_base_branch 2>/dev/null || echo "main")
branch="vertical/${feature_id}/test-plan"

git fetch origin "$base":"$base" 2>/dev/null || git fetch origin "$base"
git checkout -B "$branch" "origin/$base" 2>/dev/null || git checkout -B "$branch" "$base"

clo set current_branch "$branch"
clo set current_base_branch "$base"
echo "Prepared test-plan branch $branch (base: $base)"
