#!/bin/bash
# Check if zero-step failure rate exceeds threshold across recent batches

ISSUES_FILE=".beads/issues.jsonl"
RUNS_FILE=".beads/runs.jsonl"
THRESHOLD_PCT=30
CONSECUTIVE_THRESHOLD=3

if [ ! -f "$RUNS_FILE" ]; then
  echo "No runs file found at $RUNS_FILE"
  exit 0
fi

# Count zero-step failures in recent batches (last 3)
# A zero-step failure is a run with status failed/succeeded but 0 step results
recent_batches=$(jq -r '.batch // empty' "$RUNS_FILE" 2>/dev/null | sort -u | tail -n "$CONSECUTIVE_THRESHOLD")

if [ -z "$recent_batches" ]; then
  echo "No batch data found, skipping check"
  exit 0
fi

consecutive_high=0
for batch in $recent_batches; do
  total=$(jq -r "select(.batch == \"$batch\")" "$RUNS_FILE" 2>/dev/null | jq -s 'length')
  zero_step=$(jq -r "select(.batch == \"$batch\")" "$RUNS_FILE" 2>/dev/null | jq -s '[.[] | select((.steps // [] | length) == 0 and .status != "running")] | length')

  if [ "$total" -gt 0 ]; then
    pct=$((zero_step * 100 / total))
    echo "Batch $batch: $zero_step/$total zero-step failures ($pct%)"
    if [ "$pct" -ge "$THRESHOLD_PCT" ]; then
      consecutive_high=$((consecutive_high + 1))
    else
      consecutive_high=0
    fi
  fi
done

if [ "$consecutive_high" -ge "$CONSECUTIVE_THRESHOLD" ]; then
  echo "ALERT: Zero-step failure rate >= ${THRESHOLD_PCT}% for $consecutive_high consecutive batches"
  echo "Investigate container resource limits and Docker daemon stability"
  exit 1
fi

echo "Zero-step failure rate within acceptable range (consecutive high batches: $consecutive_high/$CONSECUTIVE_THRESHOLD)"
exit 0
