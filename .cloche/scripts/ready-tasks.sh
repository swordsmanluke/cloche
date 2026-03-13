#!/usr/bin/env bash
# ready-tasks.sh — Output open tasks as JSONL for the daemon to parse.
# Each line is a JSON object with at least: id, status, title, description.
# The daemon picks which task to assign; this script just reports what's available.
set -euo pipefail

# bd list --json outputs a JSON array; convert to JSONL (one object per line).
json=$(bd list -s open --json 2>/dev/null) || json="[]"

# Empty array means no work — exit 0 with no output (daemon sees zero tasks).
if [ "$json" = "[]" ] || [ -z "$json" ]; then
  exit 0
fi

# Convert JSON array to JSONL
echo "$json" | jq -c '.[]'
