#!/usr/bin/env bash
# vertical-close-layer.sh — finalize an approved layer (Strategy B: defer merge).
#
# Does NOT merge the PR. Leaves it approved-but-open so the stack stays coherent
# until `finalize` does a single squash-merge of the bottom-most branch into the
# user-specified base. Closes the layer task in bead and clears layer-scoped KV
# so the next layer starts fresh.
#
# Reads:
#   current_pr_number — KV (just for logging)
#   current_layer_id  — KV
set -euo pipefail

pr_number=$(cloche get current_pr_number 2>/dev/null || true)
layer_id=$(cloche get current_layer_id 2>/dev/null || true)

if [ -z "$layer_id" ]; then
  echo "error: current_layer_id missing from KV" >&2
  exit 1
fi

if bd close "$layer_id" 2>/dev/null; then
  echo "Closed layer task $layer_id (PR #${pr_number:-?} approved, kept open for stack)"
else
  echo "warning: could not close layer task $layer_id" >&2
fi

# Clear layer-scoped KV so the next layer's pick/implement starts fresh.
cloche set current_layer_id ""
cloche set current_pr_number ""
cloche set current_branch ""
cloche set current_base_branch ""
cloche set implement_status ""
cloche set last_addressed_at ""
