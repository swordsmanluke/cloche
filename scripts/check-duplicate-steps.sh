#!/bin/bash
set -euo pipefail

WORKFLOW=".cloche/develop.cloche"

if [ ! -f "$WORKFLOW" ]; then
  echo "FAIL: Workflow file not found: $WORKFLOW"
  exit 1
fi

# Extract step names (lines matching 'step <name> {' pattern)
STEP_NAMES=$(grep -oP '^\s*step\s+\K[a-zA-Z0-9_-]+' "$WORKFLOW" | sort)

if [ -z "$STEP_NAMES" ]; then
  echo "FAIL: No step definitions found in $WORKFLOW"
  exit 1
fi

DUPES=$(echo "$STEP_NAMES" | uniq -d)

if [ -n "$DUPES" ]; then
  echo "FAIL: Duplicate step definitions found in $WORKFLOW:"
  echo "$DUPES" | while read -r name; do
    echo "  - $name (appears $(echo "$STEP_NAMES" | grep -c "^${name}$") times)"
    grep -n "^\s*step\s\+${name}\b" "$WORKFLOW" | sed 's/^/    line /'
  done
  exit 1
fi

TOTAL=$(echo "$STEP_NAMES" | wc -l)
echo "OK: $TOTAL step definitions, no duplicates found in $WORKFLOW"
exit 0
