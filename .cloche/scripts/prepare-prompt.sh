#!/usr/bin/env bash
# prepare-prompt.sh — Build the task prompt from the daemon-assigned task.
# Uses CLOCHE_TASK_ID env var; details come from the tracker's claim-task outputs.
set -euo pipefail

if [ -z "${CLOCHE_TASK_ID:-}" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

# Task details come from the claim-task phase (task_title, task_body_path).
# Fallback for projects without a claim-task phase: look them up in Beads.
task_title=$(cloche get task_title 2>/dev/null || true)
task_body=""
body_path=$(cloche get task_body_path 2>/dev/null || true)
if [ -n "$body_path" ] && [ -f "$body_path" ]; then
  task_body=$(cat "$body_path")
fi
if [ -z "$task_title" ]; then
  task_json=$(bd show "$CLOCHE_TASK_ID" --json 2>/dev/null) || {
    echo "error: could not look up task $CLOCHE_TASK_ID" >&2
    exit 1
  }
  task_title=$(echo "$task_json" | jq -r '.[0].title // empty')
  task_body=$(echo "$task_json" | jq -r '.[0].description // empty')
fi

if [ -z "$task_title" ]; then
  echo "error: task $CLOCHE_TASK_ID has no title" >&2
  exit 1
fi

prompt="## Task: ${task_title}

${task_body}"

echo "$prompt"

# Write prompt to a file and store the path in KV so container steps can read it.
# (KV values are limited to 1 KB — too small for task descriptions.)
# temp_file_dir is set by the daemon at run start; its directory is pre-created.
prompt_path="$(cloche get temp_file_dir)/task_prompt.md"
echo "$prompt" > "$prompt_path"
cloche set task_prompt_path "$prompt_path"
