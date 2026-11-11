#!/usr/bin/env bash
# poll.sh — one non-blocking check of a Saga batch
# (`saga poll <batch> --no-wait --json`).
# Contract: kv saga_batch_id; also sets saga_outcome (answered|reframe). Prints:
#   CLOCHE_RESULT:answered  every question has a ruling (.done == true)
#   CLOCHE_RESULT:reframe   any question was deprecated (sent back by the judge)
#   (nothing, exit 0)       still open (saga exit 2); the poll step tries again
#   exit 1                  saga failed or the output was unusable
# Saga's JSON: {"done":bool,"questions":[{"id":"…","status":{"state":"…"}}]}.
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

saga_poll_json "$batch" || exit 1
summary=$(printf '%s' "$POLL_JSON" | jq -r '
  "\(.questions | length) \([.questions[] | select(.status.state == "deprecated")] | length) \(.done)"') || {
  echo "error: unparseable saga poll output" >&2
  exit 1
}
read -r total deprecated done_flag <<<"$summary"

if [ "$total" -eq 0 ]; then
  echo "error: batch $batch has no questions" >&2
  exit 1
fi
if [ "$deprecated" -gt 0 ]; then
  kv_set saga_outcome reframe
  echo "CLOCHE_RESULT:reframe"
elif [ "$done_flag" = "true" ]; then
  kv_set saga_outcome answered
  echo "CLOCHE_RESULT:answered"
else
  echo "batch $batch: still open"
fi
