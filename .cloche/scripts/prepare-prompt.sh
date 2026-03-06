#!/usr/bin/env bash
# Default prompt generator.
# Writes the task prompt to stdout and to $CLOCHE_STEP_OUTPUT.
set -euo pipefail

prompt="## Task: ${CLOCHE_TASK_TITLE}

${CLOCHE_TASK_BODY}"

echo "$prompt"
[ -n "${CLOCHE_STEP_OUTPUT:-}" ] && echo "$prompt" > "$CLOCHE_STEP_OUTPUT"
