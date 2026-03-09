#!/bin/bash
set -euo pipefail

WORKFLOW=".cloche/develop.cloche"

if [ ! -f "$WORKFLOW" ]; then
  echo "FAIL: workflow file not found: $WORKFLOW"
  exit 1
fi

# Collect all step names defined in the workflow
steps=$(grep -oP '(?<=^  step )\S+' "$WORKFLOW" | sed 's/ {//')

# For each step, check that some edge routes TO it (i.e. '-> stepname' appears)
# Exclude the first step (implement) which is the entry point and needs no inbound edge
entry_step=$(echo "$steps" | head -1)

orphaned=()
for s in $steps; do
  [ "$s" = "$entry_step" ] && continue
  # Check if any edge targets this step: '-> stepname' at end of line or before whitespace
  if ! grep -qP "->\s+${s}\s*$" "$WORKFLOW"; then
    orphaned+=("$s")
  fi
done

if [ ${#orphaned[@]} -gt 0 ]; then
  echo "FAIL: orphaned steps (no inbound edges):" "${orphaned[@]}"
  exit 1
fi

# Additionally verify check-vet-build-steps is wired between test:success and update-docs
if grep -qP '^\s+test:success\s+->\s+update-docs' "$WORKFLOW"; then
  echo "FAIL: test:success still routes directly to update-docs; check-vet-build-steps should be in between"
  exit 1
fi

if ! grep -qP '^\s+test:success\s+->\s+check-vet-build-steps' "$WORKFLOW"; then
  echo "FAIL: expected edge 'test:success -> check-vet-build-steps' not found"
  exit 1
fi

if ! grep -qP '^\s+check-vet-build-steps:success\s+->\s+update-docs' "$WORKFLOW"; then
  echo "FAIL: expected edge 'check-vet-build-steps:success -> update-docs' not found"
  exit 1
fi

echo "OK: no orphaned steps; check-vet-build-steps is properly wired"
exit 0
