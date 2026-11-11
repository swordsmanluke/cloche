#!/usr/bin/env bash
# create.sh — Create a task.
# Contract: kv title -> out new_task_id. Optional kv: body, priority (0-4, 0 is
# most urgent), labels (comma-separated), signal_source, signal_key. A signal
# source+key is recorded as a label so update-task can find the task again.
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
if [ -n "$source_" ]; then
  sig="signal:${source_}:$(printf %s "$key" | sha1sum | cut -c1-16)"
  # "ready" lets list-tasks hand the task out: a signal's task is meant to run.
  labels="${labels:+$labels,}$sig,ready"
  body="${body}${body:+

}Signal: ${source_} / ${key}"
fi

args=(create "$title" --silent)
[ -n "$body" ] && args+=(-d "$body")
[ -n "$priority" ] && args+=(-p "$priority")
[ -n "$labels" ] && args+=(-l "$labels")

id=$(bd "${args[@]}")
if [ -z "$id" ]; then
  echo "error: bd create returned no id" >&2
  exit 1
fi
cloche set new_task_id "$id"
echo "Created task $id"
