#!/usr/bin/env bash
# update.sh — Change an issue, or find the open issue for a signal.
# Contract: kv action -> out task_found.
#   action=update  needs target_task_id; optional comment, priority (0-4, replaces the
#                  "priority:N" label), add_labels, remove_labels
#                  (comma-separated). Sets task_found=true.
#   action=find    needs signal_source and signal_key; sets task_found=true
#                  and found_task_id when an open issue carries them, else
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
    [ -n "$comment" ] && gh issue comment --body "$comment" -- "$task_id" >/dev/null
    if [ -n "$priority" ]; then
      for p in 0 1 2 3 4; do
        [ "$p" != "$priority" ] && gh issue edit --remove-label "priority:$p" -- "$task_id" >/dev/null 2>&1 || true
      done
      gh label create "priority:$priority" --color C5DEF5 >/dev/null 2>&1 || true
      gh issue edit --add-label "priority:$priority" -- "$task_id" >/dev/null
    fi
    IFS=',' read -ra l <<< "$add"
    for x in "${l[@]}"; do
      [ -z "$x" ] && continue
      gh label create "$x" --color C5DEF5 >/dev/null 2>&1 || true
      gh issue edit --add-label "$x" -- "$task_id" >/dev/null
    done
    IFS=',' read -ra l <<< "$remove"
    for x in "${l[@]}"; do [ -n "$x" ] && gh issue edit --remove-label "$x" -- "$task_id" >/dev/null; done
    cloche set task_found true
    echo "Updated issue $task_id"
    ;;
  find)
    source_="$(cloche get signal_source)"
    key="$(cloche get signal_key)"
    if [ -z "$source_" ] || [ -z "$key" ]; then
      echo "error: signal_source and signal_key required" >&2
      exit 1
    fi
    sig="signal:${source_}:$(printf %s "$key" | sha1sum | cut -c1-16)"
    id=$(gh issue list --state open --label "$sig" --json number --limit 1 | jq -r '.[0].number // empty')
    if [ -n "$id" ]; then
      cloche set found_task_id "$id"
      cloche set task_found true
      echo "Found issue $id"
    else
      cloche set task_found false
      echo "No open issue for $source_ / $key"
    fi
    ;;
  *)
    echo "error: action must be update or find, got \"$action\"" >&2
    exit 1
    ;;
esac
