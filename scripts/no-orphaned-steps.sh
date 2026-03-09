#!/bin/bash
# Check that no orphaned steps exist in workflow definitions.
# An orphaned step is one that no edge routes to (excluding the first step).

set -euo pipefail

failed=0

for wf in .cloche/*.cloche; do
  [ -f "$wf" ] || continue

  # Extract all step names
  steps=$(grep -oP '^step\s+\K[a-zA-Z0-9_-]+' "$wf" || true)
  [ -z "$steps" ] && continue

  # Get the first step (entry point, doesn't need an incoming edge)
  first_step=$(echo "$steps" | head -1)

  # Extract all edge targets (right-hand side of -> arrows)
  # Edges look like: step-name:result -> target-name
  targets=$(grep -oP '->\s*\K[a-zA-Z0-9_-]+' "$wf" || true)

  for step in $steps; do
    [ "$step" = "$first_step" ] && continue
    if ! echo "$targets" | grep -qx "$step"; then
      echo "FAIL: $wf: step '$step' is orphaned (no edge routes to it)"
      failed=1
    fi
  done
done

if [ "$failed" -eq 0 ]; then
  echo "OK: no orphaned steps found"
fi

exit $failed
