#!/usr/bin/env bash
# read.sh — Contract: kv question_id -> out answer_path. The ruling in force; a
# human's answer is preferred over a judge's. Fails when there is no answer.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$here/lib.sh"

id="$(cloche get question_id)"
json="$(saga_show "$id")" || { echo "error: saga show $id failed" >&2; exit 1; }
# Human first, else the latest answer; deprecated answers are not in force.
text="$(printf '%s' "$json" | jq -r '
  [(.answers // [])[] | select((.status.state // "") != "deprecated")]
  | (map(select(.author.kind == "human")) | last) // last
  | .text // empty')"
if [ -z "$text" ]; then
  echo "error: no answer for question $id" >&2
  exit 1
fi
dir="${TMPDIR:-/tmp}"
path="$(mktemp "$dir/saga-answer-XXXXXX")"
printf '%s\n' "$text" > "$path"
cloche set answer_path "$path"
