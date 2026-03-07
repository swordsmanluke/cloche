#!/bin/bash
set -euo pipefail

WORKFLOW=".cloche/develop.cloche"

if [ ! -f "$WORKFLOW" ]; then
  echo "FAIL: workflow file not found: $WORKFLOW"
  exit 1
fi

errors=0

# 1. check-vet-build-wiring and check-orphaned-steps should not exist as step definitions
for removed_step in check-vet-build-wiring check-orphaned-steps; do
  if grep -qP "^\s+step ${removed_step}\b" "$WORKFLOW"; then
    echo "FAIL: redundant step '${removed_step}' still defined — remove it"
    errors=$((errors + 1))
  fi
done

# 2. check-preflight must exist and be wired as the entry point
if ! grep -qP '^\s+step check-preflight\b' "$WORKFLOW"; then
  echo "FAIL: step 'check-preflight' not defined"
  errors=$((errors + 1))
else
  # The first step in the file is the entry point; it should be check-preflight
  entry_step=$(grep -oP '(?<=^\s{2}step )\S+' "$WORKFLOW" | head -1)
  if [ "$entry_step" != "check-preflight" ]; then
    echo "FAIL: first step should be 'check-preflight' (entry point), got '${entry_step}'"
    errors=$((errors + 1))
  fi
  # check-preflight:success -> implement
  if ! grep -qP '^\s+check-preflight:success\s+->\s+implement' "$WORKFLOW"; then
    echo "FAIL: expected edge 'check-preflight:success -> implement' not found"
    errors=$((errors + 1))
  fi
  # check-preflight:fail -> abort
  if ! grep -qP '^\s+check-preflight:fail\s+->\s+abort' "$WORKFLOW"; then
    echo "FAIL: expected edge 'check-preflight:fail -> abort' not found"
    errors=$((errors + 1))
  fi
fi

# 3. check-vet-build-steps must be wired between test:success and update-docs
if ! grep -qP '^\s+step check-vet-build-steps\b' "$WORKFLOW"; then
  echo "FAIL: step 'check-vet-build-steps' not defined"
  errors=$((errors + 1))
else
  if grep -qP '^\s+test:success\s+->\s+update-docs' "$WORKFLOW"; then
    echo "FAIL: test:success still routes directly to update-docs; check-vet-build-steps should be in between"
    errors=$((errors + 1))
  fi
  if ! grep -qP '^\s+test:success\s+->\s+check-vet-build-steps' "$WORKFLOW"; then
    echo "FAIL: expected edge 'test:success -> check-vet-build-steps' not found"
    errors=$((errors + 1))
  fi
  if ! grep -qP '^\s+check-vet-build-steps:success\s+->\s+update-docs' "$WORKFLOW"; then
    echo "FAIL: expected edge 'check-vet-build-steps:success -> update-docs' not found"
    errors=$((errors + 1))
  fi
  if ! grep -qP '^\s+check-vet-build-steps:fail\s+->\s+fix' "$WORKFLOW"; then
    echo "FAIL: expected edge 'check-vet-build-steps:fail -> fix' not found"
    errors=$((errors + 1))
  fi
fi

# 4. General orphan check: every defined step must have at least one inbound edge
#    (except the entry point which is the first step)
all_steps=$(grep -oP '(?<=^\s{2}step )\S+' "$WORKFLOW")
entry_step=$(echo "$all_steps" | head -1)

for s in $all_steps; do
  [ "$s" = "$entry_step" ] && continue
  if ! grep -qP "->\s+${s}\s*$" "$WORKFLOW"; then
    echo "FAIL: orphaned step '${s}' — no inbound edges"
    errors=$((errors + 1))
  fi
done

if [ "$errors" -gt 0 ]; then
  echo "FAIL: ${errors} issue(s) found"
  exit 1
fi

echo "OK: no orphaned steps; all steps properly wired into main flow"
exit 0