#!/bin/bash
set -euo pipefail

WORKFLOW=".cloche/develop.cloche"

if [ ! -f "$WORKFLOW" ]; then
  echo "FAIL: workflow file not found: $WORKFLOW"
  exit 1
fi

errors=0

# check-vet-build-steps must be reachable: test:success should route to it
if ! grep -qE '^[[:space:]]*test:success[[:space:]]*->[[:space:]]*check-vet-build-steps' "$WORKFLOW"; then
  echo "FAIL: missing edge 'test:success -> check-vet-build-steps'"
  errors=$((errors + 1))
fi

# check-vet-build-steps:success should route to update-docs
if ! grep -qE '^[[:space:]]*check-vet-build-steps:success[[:space:]]*->[[:space:]]*update-docs' "$WORKFLOW"; then
  echo "FAIL: missing edge 'check-vet-build-steps:success -> update-docs'"
  errors=$((errors + 1))
fi

# check-vet-build-steps:fail should route to fix
if ! grep -qE '^[[:space:]]*check-vet-build-steps:fail[[:space:]]*->[[:space:]]*fix' "$WORKFLOW"; then
  echo "FAIL: missing edge 'check-vet-build-steps:fail -> fix'"
  errors=$((errors + 1))
fi

# test:success should NOT route directly to update-docs (old wiring)
if grep -qE '^[[:space:]]*test:success[[:space:]]*->[[:space:]]*update-docs' "$WORKFLOW"; then
  echo "FAIL: stale edge 'test:success -> update-docs' still present (should go through check-vet-build-steps)"
  errors=$((errors + 1))
fi

if [ "$errors" -gt 0 ]; then
  echo "$errors wiring error(s) found"
  exit 1
fi

echo "OK: check-vet-build-steps is correctly wired between test and update-docs"
exit 0
