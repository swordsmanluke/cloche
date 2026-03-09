#!/usr/bin/env bash
# Default prompt generator.
# Writes the task prompt to stdout and to $CLOCHE_STEP_OUTPUT.
# Expects CLOCHE_TASK_TITLE and CLOCHE_TASK_BODY from wire output mappings.
set -euo pipefail

task_title="${CLOCHE_TASK_TITLE:-}"
task_body="${CLOCHE_TASK_BODY:-}"

if [ -z "$task_title" ]; then
  echo "error: CLOCHE_TASK_TITLE is not set" >&2
  exit 1
fi

prompt="## Task: ${task_title}

${task_body}"

echo "$prompt"
[ -n "${CLOCHE_STEP_OUTPUT:-}" ] && echo "$prompt" > "$CLOCHE_STEP_OUTPUT"
