#!/bin/bash
# Check that the rate of runs where implement starts but never completes
# (indicating agent crashes or container-level failures) stays below 25%.

set -euo pipefail

THRESHOLD=25

# Count total runs and runs where implement started but produced no result
total=0
crashed=0

for run_dir in .cloche/*/; do
  [ -d "$run_dir" ] || continue
  # Skip if not a run directory (must have a status file or log)
  log_file="$run_dir/agent.log"
  [ -f "$log_file" ] || continue

  total=$((total + 1))

  # Check if implement step started
  if grep -q 'implement.*start\|Starting.*implement\|step.*implement' "$log_file" 2>/dev/null; then
    # Check if implement step produced a result
    if ! grep -q 'implement.*result\|implement.*completed\|implement.*succeeded\|implement.*done' "$log_file" 2>/dev/null; then
      crashed=$((crashed + 1))
    fi
  fi
done

if [ "$total" -eq 0 ]; then
  echo "No runs found to analyze."
  exit 0
fi

rate=$((crashed * 100 / total))

echo "Agent crash rate: $crashed / $total runs ($rate%)"
echo "Threshold: ${THRESHOLD}%"

if [ "$rate" -gt "$THRESHOLD" ]; then
  echo "FAIL: Agent crash rate ${rate}% exceeds ${THRESHOLD}% threshold."
  echo "Investigate container resource limits and agent timeout configuration."
  exit 1
fi

echo "OK: Agent crash rate is within acceptable range."
exit 0
