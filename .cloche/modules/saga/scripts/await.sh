#!/usr/bin/env bash
# await.sh — Contract: kv question_ids -> out questions_open. One non-blocking
# check: lists the ids that have no answer yet (empty when all are answered).
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$here/lib.sh"

open=""
for id in $(cloche get question_ids); do
  json="$(saga_show "$id")" || { echo "error: saga show $id failed" >&2; exit 1; }
  n="$(printf '%s' "$json" | jq '(.answers // []) | length')" || { echo "error: unparseable saga show output for $id" >&2; exit 1; }
  if [ "$n" -eq 0 ]; then
    open="${open:+$open
}$id"
  fi
done
cloche set questions_open "$open"
