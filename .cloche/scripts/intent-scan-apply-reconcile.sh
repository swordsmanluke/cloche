#!/usr/bin/env bash
# intent-scan-apply-reconcile.sh — Apply the reconcile step's reconcile.json
# to .cloche/intent/. This is the "post-reconcile validation script" from the
# design: the reconcile agent only proposes create/merge/supersede/drop
# decisions in reconcile.json, and this deterministic step (via `cloche
# intent apply-reconcile`) is what actually writes files, enforcing the hard
# rules (never delete, never re-enable disabled, never rewrite a user_edited
# requirement in place) atomically — if any action violates a rule, nothing
# in the batch is applied.
set -euo pipefail

PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)

if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi

RECONCILE_FILE="$TEMP/reconcile.json"
if [ ! -f "$RECONCILE_FILE" ]; then
  echo "error: $RECONCILE_FILE not found — did the reconcile step write it?" >&2
  exit 1
fi

cloche intent apply-reconcile --project "$PROJECT_DIR" --reconcile-file "$RECONCILE_FILE"
