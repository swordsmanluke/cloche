#!/bin/bash
# Check infrastructure stability: failure rate should be within acceptable baseline (<=15%)

set -euo pipefail

LOG_DIR="${1:-.cloche}"
THRESHOLD=15

# Count total runs and failed runs (zero-step failures)
total=0
failed=0

for run_dir in "$LOG_DIR"/*/; do
  [ -d "$run_dir" ] || continue
  # Skip non-run directories
  basename="$(basename "$run_dir")"
  case "$basename" in
    prompts|overrides) continue ;;
  esac
  total=$((total + 1))
  # A run with zero completed steps is considered a failure
  step_count=$(find "$run_dir" -name '*.result' -o -name '*.log' 2>/dev/null | head -1)
  if [ -z "$step_count" ]; then
    failed=$((failed + 1))
  fi
done

if [ "$total" -eq 0 ]; then
  echo "No runs found to evaluate. Skipping stability check."
  exit 0
fi

rate=$((failed * 100 / total))

echo "Infrastructure stability: $failed/$total runs failed ($rate%)"
echo "Threshold: ${THRESHOLD}%"

if [ "$rate" -gt "$THRESHOLD" ]; then
  echo "FAIL: Failure rate $rate% exceeds ${THRESHOLD}% threshold"
  exit 1
fi

echo "PASS: Failure rate is within acceptable baseline"
exit 0
