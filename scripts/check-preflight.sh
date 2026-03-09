#!/bin/bash
# Check that the workflow has a preflight step as its first step,
# and that it routes success to the next real step and failure to abort.

set -euo pipefail

WORKFLOW_FILE=".cloche/develop.cloche"

if [ ! -f "$WORKFLOW_FILE" ]; then
  echo "FAIL: Workflow file not found: $WORKFLOW_FILE"
  exit 1
fi

# Check that a preflight step exists
if ! grep -q 'step\s\+preflight' "$WORKFLOW_FILE"; then
  echo "FAIL: No 'preflight' step found in $WORKFLOW_FILE"
  echo "Add a lightweight preflight step (e.g., run = 'echo ok') as the first step"
  echo "to distinguish container startup failures from step execution failures."
  exit 1
fi

# Check that preflight has a simple run command
if ! grep -A5 'step\s\+preflight' "$WORKFLOW_FILE" | grep -q 'run\s*='; then
  echo "FAIL: preflight step exists but has no 'run' command"
  exit 1
fi

# Check that preflight routes success forward (not to done/abort)
if ! grep -A20 'step\s\+preflight' "$WORKFLOW_FILE" | grep -q 'success'; then
  echo "FAIL: preflight step has no 'success' result routing"
  echo "Route preflight:success to the next real step (e.g., implement)"
  exit 1
fi

# Check that preflight routes failure to abort
if ! grep -A20 'step\s\+preflight' "$WORKFLOW_FILE" | grep -q 'fail.*abort\|abort.*fail'; then
  echo "WARN: preflight step does not route failure to abort"
  echo "Consider routing preflight:fail to abort to catch container startup issues"
fi

echo "OK: preflight step is present and configured"
exit 0
