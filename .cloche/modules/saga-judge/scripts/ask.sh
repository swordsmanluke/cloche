#!/usr/bin/env bash
# ask.sh — run `saga ask` on a batch file and store the batch id in KV.
# Contract: kv saga_batch_file (path to the batch file) -> out saga_batch_id.
# The batch id is the last token on stdout shaped like CLOCHE-B23 (override the
# pattern with SAGA_BATCH_ID_PATTERN).
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

out=$("$SAGA_BIN" ask "$file") || {
  echo "error: saga ask failed" >&2
  exit 1
}
pattern="${SAGA_BATCH_ID_PATTERN:-[A-Za-z][A-Za-z0-9]*-B[0-9]+}"
id=$(printf '%s\n' "$out" | grep -Eo "$pattern" | tail -n 1 || true)
if [ -z "$id" ]; then
  echo "error: could not find a batch id in saga ask output:" >&2
  printf '%s\n' "$out" >&2
  exit 1
fi
kv_set saga_batch_id "$id"
echo "Asked batch $id"
