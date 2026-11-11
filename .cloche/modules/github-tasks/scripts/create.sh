#!/usr/bin/env bash
# create.sh — Create a GitHub issue.
# Contract: kv title -> out new_task_id (the issue number). Optional kv: body,
# priority (0-4, 0 most urgent; becomes a "priority:N" label), labels
# (comma-separated), signal_source, signal_key (recorded as a label so
# update-task can find the issue again).
set -euo pipefail

opt() { cloche get "$1" 2>/dev/null || true; }

title="$(cloche get title)"
if [ -z "$title" ]; then
  echo "error: title not set" >&2
  exit 1
fi
body="$(opt body)"
priority="$(opt priority)"
labels="$(opt labels)"
source_="$(opt signal_source)"
key="$(opt signal_key)"

# Priority 0 marks an urgent task: the "urgent" label lets the daemon launch it
# in an emergency slot (see [orchestration] emergency).
if [ "$priority" = "0" ]; then
  labels="${labels:+$labels,}urgent"
fi

if [ -n "$key" ] && [ -z "$source_" ]; then
  echo "error: signal_key requires signal_source" >&2
  exit 1
fi
[ -n "$priority" ] && labels="${labels:+$labels,}priority:$priority"
if [ -n "$source_" ]; then
  sig="signal:${source_}:$(printf %s "$key" | sha1sum | cut -c1-16)"
  labels="${labels:+$labels,}$sig"
  body="${body}${body:+

}Signal: ${source_} / ${key}"
fi

# Create labels on first use; ignore "already exists".
IFS=',' read -ra l <<< "$labels"
args=(issue create --title "$title" --body "$body")
for x in "${l[@]}"; do
  [ -z "$x" ] && continue
  gh label create "$x" --color C5DEF5 >/dev/null 2>&1 || true
  args+=(--label "$x")
done

url=$(gh "${args[@]}")
id="${url##*/}"
if [ -z "$id" ]; then
  echo "error: gh issue create returned no issue" >&2
  exit 1
fi
cloche set new_task_id "$id"
echo "Created issue $id"
