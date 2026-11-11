#!/usr/bin/env bash
# judge-prepare.sh — list the open, not-held questions of the batch for the judge.
# Writes <temp_file_dir>/judge-questions.json and sets judge_questions_path.
# Fails if the batch id is missing.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$here/lib.sh"

batch="${SAGA_BATCH_ID:-$(kv_get saga_batch_id)}"
if [ -z "$batch" ]; then
  echo "error: saga_batch_id not set" >&2
  exit 1
fi
dir="${SAGA_JUDGE_TMP:-$(kv_get temp_file_dir)}"
dir="${dir:-.}"
mkdir -p "$dir"
out="$dir/judge-questions.json"
"$SAGA_BIN" poll --no-wait "$batch" \
  | jq '[.questions[] | select(.status == "open")]' > "$out"
kv_set judge_questions_path "$out"
echo "$(jq length "$out") open question(s) in $batch"
