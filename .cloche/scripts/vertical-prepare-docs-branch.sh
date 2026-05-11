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

# The bottom-most closed layer is recorded by the host's vertical-close-layer.sh
# step as it closes each layer. We read it from KV here because the agent
# image doesn't ship the bd CLI.
last_layer=$(clo get last_closed_layer_id 2>/dev/null || true)
if [ -z "$last_layer" ]; then
  echo "error: last_closed_layer_id not set in KV — did close-layer run?" >&2
  exit 1
fi

base="vertical/${feature_id}/${last_layer}"
branch="vertical/${feature_id}/docs"

git fetch origin "$base":"$base" 2>/dev/null || git fetch origin "$base"
git checkout -B "$branch" "origin/$base" 2>/dev/null || git checkout -B "$branch" "$base"

clo set current_branch "$branch"
clo set current_base_branch "$base"
echo "Prepared docs branch $branch (base: $base)"
