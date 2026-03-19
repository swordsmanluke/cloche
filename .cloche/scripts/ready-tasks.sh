#!/usr/bin/env bash
# ready-tasks.sh — Output truly ready tasks as JSONL for the daemon to parse.
# A task is ready when:
#   1. bd considers it ready (open, not blocked/deferred)
#   2. All its closed dependencies have a succeeded cloche run
set -uo pipefail

# bd ready --json outputs a JSON array of ready tasks.
json=$(bd ready --json 2>/dev/null) || json="[]"

if [ "$json" = "[]" ] || [ -z "$json" ]; then
  exit 0
fi

task_count=$(echo "$json" | jq 'length')

for (( i=0; i<task_count; i++ )); do
  task=$(echo "$json" | jq -c ".[$i]")
  task_id=$(echo "$json" | jq -r ".[$i].id")

  # Get closed dependency IDs
  deps=$(bd show "$task_id" --json 2>/dev/null \
    | jq -r '.[0].dependencies[]? | select(.status == "closed") | .id' 2>/dev/null) || deps=""

  ready=true
  if [ -n "$deps" ]; then
    while IFS= read -r dep_id; do
      [ -z "$dep_id" ] && continue
      count=$(cloche list --all --issue "$dep_id" --state succeeded 2>/dev/null | grep -c "succeeded" || true)
      if [ "$count" -eq 0 ]; then
        ready=false
        break
      fi
    done <<< "$deps"
  fi

  if [ "$ready" = true ]; then
    echo "$task"
  fi
done
