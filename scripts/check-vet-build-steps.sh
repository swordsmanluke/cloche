#!/bin/bash
# Check that the workflow includes vet and build validation steps

WORKFLOW_FILE=".cloche/develop.cloche"

if [ ! -f "$WORKFLOW_FILE" ]; then
  echo "FAIL: Workflow file not found: $WORKFLOW_FILE"
  exit 1
fi

errors=0

# Check for vet step
if ! grep -q 'step "vet"' "$WORKFLOW_FILE"; then
  echo "FAIL: Missing 'vet' step in workflow"
  errors=$((errors + 1))
else
  echo "OK: Found 'vet' step"
  if ! grep -A5 'step "vet"' "$WORKFLOW_FILE" | grep -q 'go vet'; then
    echo "FAIL: 'vet' step does not run 'go vet'"
    errors=$((errors + 1))
  else
    echo "OK: 'vet' step runs 'go vet'"
  fi
fi

# Check for build step
if ! grep -q 'step "build"' "$WORKFLOW_FILE"; then
  echo "FAIL: Missing 'build' step in workflow"
  errors=$((errors + 1))
else
  echo "OK: Found 'build' step"
  if ! grep -A5 'step "build"' "$WORKFLOW_FILE" | grep -q 'go build'; then
    echo "FAIL: 'build' step does not run 'go build'"
    errors=$((errors + 1))
  else
    echo "OK: 'build' step runs 'go build'"
  fi
fi

# Check that vet/build failures route to fix
for step_name in vet build; do
  if grep -A10 "step \"$step_name\"" "$WORKFLOW_FILE" | grep -q 'fix'; then
    echo "OK: '$step_name' failure routes to fix step"
  else
    echo "FAIL: '$step_name' step does not route failures to fix step"
    errors=$((errors + 1))
  fi
done

if [ $errors -gt 0 ]; then
  echo "\nFAILED: $errors check(s) failed"
  exit 1
fi

echo "\nAll checks passed"
exit 0