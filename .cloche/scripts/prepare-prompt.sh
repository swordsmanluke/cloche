#!/usr/bin/env bash
# Default prompt generator.
# Writes a JSON object with the task prompt and metadata to stdout and to
# $CLOCHE_STEP_OUTPUT.  Downstream wire mappings can extract individual fields.
set -euo pipefail

task_id="${CLOCHE_TASK_ID:-}"
task_title="${CLOCHE_TASK_TITLE:-}"
task_body="${CLOCHE_TASK_BODY:-}"

if [ -z "$task_title" ]; then
  echo "error: CLOCHE_TASK_TITLE is not set" >&2
  exit 1
fi

prompt="## Task: ${task_title}

${task_body}"

output=$(jq -n \
  --arg prompt "$prompt" \
  --arg task_id "$task_id" \
  '{prompt: $prompt, task_id: $task_id}')

echo "$output"
[ -n "${CLOCHE_STEP_OUTPUT:-}" ] && echo "$output" > "$CLOCHE_STEP_OUTPUT"
