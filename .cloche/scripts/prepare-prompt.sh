#!/usr/bin/env bash
# Default prompt generator.
# Writes the task prompt to stdout and to $CLOCHE_STEP_OUTPUT.
# Reads task data from run context (set by ready-tasks.sh via cloche set).
set -euo pipefail

task_title=$(cloche get task_title)
task_body=$(cloche get task_body)

if [ -z "$task_title" ]; then
  echo "error: task_title not found in run context" >&2
  exit 1
fi

prompt="## Task: ${task_title}

${task_body}"

echo "$prompt"
[ -n "${CLOCHE_STEP_OUTPUT:-}" ] && echo "$prompt" > "$CLOCHE_STEP_OUTPUT"
