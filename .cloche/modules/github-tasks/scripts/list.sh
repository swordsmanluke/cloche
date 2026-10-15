#!/usr/bin/env bash
# list.sh — Write open, unclaimed GitHub issues as task JSONL and publish the
# file's path as the tasks_path output. Issues labelled with the in-progress
# label (default cloche:in-progress) are already claimed and are skipped.
set -uo pipefail

label="${CLOCHE_GH_CLAIM_LABEL:-cloche:in-progress}"
out="$PWD/.cloche/runs/list-tasks.jsonl"
mkdir -p "$(dirname "$out")"
: > "$out"
cloche set tasks_path "$out"

json=$(gh issue list --state open --limit 100 --json number,title,body,labels 2>/dev/null) || {
  echo "error: gh issue list failed" >&2
  exit 1
}

echo "$json" | jq -c --arg label "$label" '
  .[]
  | select(([.labels[].name] | index($label)) == null)
  | {id: (.number | tostring), status: "open", title: .title, description: (.body // "")}
' >> "$out"
