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
# A choice answer may carry "select" (1-based option positions) and/or text:
# print "Selected: <labels>" then the text; empty only if it has neither.
text="$(printf '%s' "$json" | jq -r '
  def author: (.author | if type == "object" then .kind else . end) // "";
  def state: (.status | if type == "object" then .state else . end) // "";
  . as $q
  | [(.answers // [])[] | select(state != "deprecated")]
  | ((map(select(author == "human")) | last) // last)
  | select(. != null)
  | ([((.select // [])[] as $p | ($q.options // [])[] | select(.position == $p) | .label)] | join(", ")) as $sel
  | [(if $sel != "" then "Selected: " + $sel else empty end), (.text // empty)]
  | join("\n")')"
if [ -z "$text" ]; then
  echo "error: no answer for question $id" >&2
  exit 1
fi
dir="${TMPDIR:-/tmp}"
path="$(mktemp "$dir/saga-answer-XXXXXX")"
printf '%s\n' "$text" > "$path"
cloche set answer_path "$path"
