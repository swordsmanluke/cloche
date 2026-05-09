#!/usr/bin/env bash
# vertical-prepare-docs-branch.sh — create the docs branch off the bottom-most
# layer's branch (the top of the stack).
#
# Runs in-container.
set -euo pipefail

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

# Find the most recent (bottom-most-stack-position) closed child layer task.
# Children are returned oldest-first; we want the latest closed one.
layers_json=$(bd list --parent "$feature_id" --all --json 2>/dev/null) || layers_json="[]"
last_layer=$(echo "$layers_json" | jq -r '[.[] | select(.status == "closed")] | last.id // empty')

if [ -z "$last_layer" ]; then
  echo "error: no closed layer tasks found for feature $feature_id" >&2
  exit 1
fi

base="vertical/${feature_id}/${last_layer}"
branch="vertical/${feature_id}/docs"

git fetch origin "$base":"$base" 2>/dev/null || git fetch origin "$base"
git checkout -B "$branch" "origin/$base" 2>/dev/null || git checkout -B "$branch" "$base"

clo set current_branch "$branch"
clo set current_base_branch "$base"
echo "Prepared docs branch $branch (base: $base)"
