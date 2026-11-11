#!/usr/bin/env bash
# update.sh — Change a task, or find the open task for a signal.
# Contract: kv action -> out task_found.
#   action=update  needs target_task_id; optional comment, priority, add_labels,
#                  remove_labels (comma-separated). Sets task_found=true.
#   action=find    needs signal_source and signal_key; sets task_found=true
#                  and found_task_id when an open task carries them, else
#                  task_found=false (still success).
set -euo pipefail

opt() { cloche get "$1" 2>/dev/null || true; }

action="$(cloche get action)"
case "$action" in
  update)
    task_id="$(opt target_task_id)"
    if [ -z "$task_id" ]; then
      echo "error: target_task_id not set" >&2
      exit 1
    fi
    comment="$(opt comment)"
    priority="$(opt priority)"
    add="$(opt add_labels)"
    remove="$(opt remove_labels)"
    if [ -z "$comment$priority$add$remove" ]; then
      echo "error: nothing to update (set comment, priority, add_labels or remove_labels)" >&2
      exit 1
    fi
    [ -n "$comment" ] && bd comments add -- "$task_id" "$comment" >/dev/null
    [ -n "$priority" ] && bd update -p "$priority" -- "$task_id" >/dev/null
    IFS=',' read -ra l <<< "$add"
    for x in "${l[@]}"; do [ -n "$x" ] && bd label add -- "$task_id" "$x" >/dev/null; done
    IFS=',' read -ra l <<< "$remove"
    for x in "${l[@]}"; do [ -n "$x" ] && bd label remove -- "$task_id" "$x" >/dev/null; done
    cloche set task_found true
    echo "Updated task $task_id"
    ;;
  find)
    source_="$(cloche get signal_source)"
    key="$(cloche get signal_key)"
    if [ -z "$source_" ] || [ -z "$key" ]; then
      echo "error: signal_source and signal_key required" >&2
      exit 1
    fi
    sig="signal:${source_}:$(printf %s "$key" | sha1sum | cut -c1-16)"
    id=$(bd list --label "$sig" --json 2>/dev/null \
      | jq -r '[.[] | select(.status != "closed")][0].id // empty') || id=""
    if [ -n "$id" ]; then
      cloche set found_task_id "$id"
      cloche set task_found true
      echo "Found task $id"
    else
      cloche set task_found false
      echo "No open task for $source_ / $key"
    fi
    ;;
  *)
    echo "error: action must be update or find, got \"$action\"" >&2
    exit 1
    ;;
esac
