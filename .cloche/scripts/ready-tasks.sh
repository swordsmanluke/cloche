#!/usr/bin/env bash
# ready-tasks.sh — Output truly ready tasks as JSONL for the daemon to parse.
# A task is ready when:
#   1. bd considers it ready (open, not blocked/deferred)
#   2. All its closed dependencies have a succeeded cloche run
set -euo pipefail

# bd ready --json outputs a JSON array of ready tasks.
json=$(bd ready --json 2>/dev/null) || json="[]"

if [ "$json" = "[]" ] || [ -z "$json" ]; then
  exit 0
fi

# For each ready task, verify that all closed dependencies have landed.
echo "$json" | jq -c '.[]' | while IFS= read -r task; do
  task_id=$(echo "$task" | jq -r '.id')

  # Get closed dependencies for this task (bd show --json returns an array)
  closed_deps=$(bd show "$task_id" --json 2>/dev/null \
    | jq -r '.[0].dependencies[]? | select(.status == "closed") | .id' 2>/dev/null) || true

  ready=true
  for dep_id in $closed_deps; do
    # Check if cloche has a succeeded run for this dependency
    if ! cloche list --all --issue "$dep_id" --state succeeded 2>/dev/null | grep -q "succeeded"; then
      ready=false
      break
    fi
  done

  if [ "$ready" = true ]; then
    echo "$task"
  fi
done
