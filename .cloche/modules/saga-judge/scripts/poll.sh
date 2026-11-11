#!/usr/bin/env bash
# poll.sh — one non-blocking check of a Saga batch (`saga poll --no-wait`).
# Contract: kv saga_batch_id; also sets saga_outcome (answered|reframe). Prints:
#   CLOCHE_RESULT:answered  every question has a ruling
#   CLOCHE_RESULT:reframe   any question was deprecated (sent back by the judge)
#   (nothing, exit 0)       still pending; the poll step tries again
# `saga poll --no-wait <batch>` is expected to print JSON:
#   {"questions":[{"id":"...","status":"open|held|answered|deprecated"}]}
# Reframe wins over pending: a sent-back question needs a re-ask now, not after
# the other questions are answered.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$here/lib.sh"

batch="${SAGA_BATCH_ID:-$(kv_get saga_batch_id)}"
if [ -z "$batch" ]; then
  echo "error: saga_batch_id not set" >&2
  exit 1
fi

json=$("$SAGA_BIN" poll --no-wait "$batch") || {
  echo "error: saga poll failed" >&2
  exit 1
}
counts=$(printf '%s' "$json" | jq -r '
  [.questions[]?.status] as $s
  | "\($s | length) \($s | map(select(. == "deprecated")) | length) \($s | map(select(. == "answered")) | length)"') || {
  echo "error: unparseable saga poll output" >&2
  exit 1
}
read -r total deprecated answered <<<"$counts"

if [ "$total" -eq 0 ]; then
  echo "error: batch $batch has no questions" >&2
  exit 1
fi
if [ "$deprecated" -gt 0 ]; then
  kv_set saga_outcome reframe
  echo "CLOCHE_RESULT:reframe"
elif [ "$answered" -eq "$total" ]; then
  kv_set saga_outcome answered
  echo "CLOCHE_RESULT:answered"
else
  echo "batch $batch: $answered of $total answered"
fi
