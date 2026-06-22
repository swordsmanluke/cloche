#!/usr/bin/env bash
# vertical-prepare-test-plan-branch.sh — create the test-plan branch.
#
# Base priority:
#   1. design_branch (KV) — set when Phase 0.5 ran and the design PR was approved.
#   2. vertical_base_branch (KV) — for the has-design shortcut (design already exists).
#   3. "main" — fallback.
#
# Runs in-container.
set -euo pipefail

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

# Use design_branch when Phase 0.5 ran; fall back to vertical_base_branch or "main".
base=$(clo get design_branch 2>/dev/null || true)
if [ -z "$base" ]; then
  base=$(clo get vertical_base_branch 2>/dev/null || echo "main")
fi

branch="vertical/${feature_id}/test-plan"

git fetch origin "$base":"$base" 2>/dev/null || git fetch origin "$base"
git checkout -B "$branch" "origin/$base" 2>/dev/null || git checkout -B "$branch" "$base"

clo set current_branch "$branch"
clo set current_base_branch "$base"
echo "Prepared test-plan branch $branch (base: $base)"
