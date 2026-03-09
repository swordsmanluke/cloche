#!/usr/bin/env bash
# Returns IDs of ready tasks from bd, one per line.
# Usage: ready-tasks.sh [max]
#   max  — maximum number of IDs to return (default: all)
set -euo pipefail

max="${1:-0}"

limit_args=()
if [ "$max" -gt 0 ] 2>/dev/null; then
  limit_args=(--limit "$max")
fi

json=$(bd ready --json "${limit_args[@]}" 2>/dev/null) || json="[]"

echo "$json" | jq -r '.[].id // empty'
