#!/usr/bin/env bash
# ask.sh — run `saga ask` on a batch file and store the batch id in KV.
# Contract: kv saga_batch_file (path to the batch file) -> out saga_batch_id.
# Runs: saga ask -p <project> -f <feature> --file <file> --json
# Project and feature come from kv saga_project / saga_feature, else the
# SAGA_PROJECT / SAGA_FEATURE env vars. The batch id is read from the JSON output.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$here/lib.sh"

file="${SAGA_BATCH_FILE:-$(kv_get saga_batch_file)}"
if [ -z "$file" ]; then
  echo "error: saga_batch_file not set" >&2
  exit 1
fi
if [ ! -f "$file" ]; then
  echo "error: batch file not found: $file" >&2
  exit 1
fi
project="${SAGA_PROJECT:-$(kv_get saga_project)}"
feature="${SAGA_FEATURE:-$(kv_get saga_feature)}"
if [ -z "$project" ] || [ -z "$feature" ]; then
  echo "error: saga project and feature are required: set SAGA_PROJECT and SAGA_FEATURE (or kv saga_project / saga_feature)" >&2
  exit 1
fi

out=$("$SAGA_BIN" ask -p "$project" -f "$feature" --file "$file" --json) || {
  echo "error: saga ask failed" >&2
  exit 1
}
id=$(printf '%s' "$out" | jq -r '(.batch | if type == "object" then .id else . end) // .batch_id // .id // empty' 2>/dev/null || true)
if [ -z "$id" ]; then
  echo "error: could not find a batch id in saga ask JSON output:" >&2
  printf '%s\n' "$out" >&2
  exit 1
fi
kv_set saga_batch_id "$id"
echo "Asked batch $id"
