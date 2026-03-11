#!/usr/bin/env bash
# Returns ready, unclaimed tasks from bd as a JSON array.
# Usage: ready-tasks.sh [max]
#   max  — maximum number of IDs to return (default: all)
set -euo pipefail

max="${1:-0}"

limit_args=()
if [ "$max" -gt 0 ] 2>/dev/null; then
  limit_args=(--limit "$max")
fi

# Filter to open status only — excludes in_progress (already claimed) tasks.
json=$(bd list -s open --json "${limit_args[@]}" 2>/dev/null) || json="[]"

# Empty array means no work — exit non-zero so the workflow takes the fail path
# instead of trying to resolve output[0] mappings on an empty list.
if [ "$json" = "[]" ] || [ -z "$json" ]; then
  echo "[]"
  exit 1
fi

echo "$json"
