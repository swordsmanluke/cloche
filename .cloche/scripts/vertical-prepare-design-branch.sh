#!/usr/bin/env bash
# vertical-prepare-design-branch.sh — create the design branch off vertical_base_branch.
#
# Reads (KV):
#   vertical_base_branch — base to branch from (default "main")
# Writes (KV):
#   design_branch, current_branch, current_base_branch
set -euo pipefail
exec 2>&1
trap 'rc=$?; echo "FATAL: script failed at line $LINENO (rc=$rc): $BASH_COMMAND"; exit $rc' ERR
source "$(dirname "${BASH_SOURCE[0]}")/lib/agent-creds.sh"

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

base=$(cloche get vertical_base_branch 2>/dev/null || echo "main")
branch="vertical/${feature_id}/design"

git fetch origin "$base":"$base" 2>/dev/null || git fetch origin "$base"
git checkout -B "$branch" "origin/$base" 2>/dev/null || git checkout -B "$branch" "$base"

cloche set design_branch "$branch"
cloche set current_branch "$branch"
cloche set current_base_branch "$base"
echo "Prepared design branch $branch (base: $base)"
