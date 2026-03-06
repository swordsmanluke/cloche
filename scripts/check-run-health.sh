#!/bin/bash
set -euo pipefail

# Check that recent runs actually execute steps and record results.
# Fails if the majority of runs have zero step results, indicating a
# systemic container startup or infrastructure issue.

RUN_DIR=".cloche"
THRESHOLD=50  # fail if more than this % of runs have zero results

total=0
failed=0

for run in "$RUN_DIR"/develop-*/; do
  [ -d "$run" ] || continue
  total=$((total + 1))

  # Check for any step result files (status.json, result.json, or capture files)
  has_results=false
  if [ -f "$run/status.json" ]; then
    # Check if any steps recorded a result
    if grep -q '"step_results"' "$run/status.json" 2>/dev/null; then
      step_count=$(grep -o '"step_results"' "$run/status.json" | wc -l)
      if [ "$step_count" -gt 0 ]; then
        has_results=true
      fi
    fi
  fi

  if [ "$has_results" = false ]; then
    failed=$((failed + 1))
  fi
done

if [ "$total" -eq 0 ]; then
  echo "No runs found in $RUN_DIR — nothing to check."
  exit 0
fi

pct=$((failed * 100 / total))
echo "Run health: $failed/$total runs ($pct%) have zero step results."

if [ "$pct" -gt "$THRESHOLD" ]; then
  echo "FAIL: $pct% of runs failed before any step executed (threshold: $THRESHOLD%)."
  echo "This indicates a systemic container startup or infrastructure issue."
  echo ""
  echo "Troubleshooting:"
  echo "  1. Check daemon logs: journalctl -u cloched or cloched stderr"
  echo "  2. Verify Docker is running: docker info"
  echo "  3. Test container startup manually: docker run --rm cloche-agent echo ok"
  echo "  4. Check .cloche/Dockerfile builds: docker build -f .cloche/Dockerfile ."
  exit 1
fi

echo "OK: within acceptable failure threshold."
exit 0
