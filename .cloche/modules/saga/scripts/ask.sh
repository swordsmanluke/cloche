#!/usr/bin/env bash
# ask.sh — Contract: kv questions_file -> out question_ids (one id per line, in order).
# Posts the whole file as one `saga ask` batch. Project: $SAGA_PROJECT (bind
# settings). Feature: the file's backend.feature, else $SAGA_FEATURE.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=lib.sh
. "$here/lib.sh"

file="$(cloche get questions_file)"
[ -f "$file" ] || { echo "error: questions file not found: $file" >&2; exit 1; }
project="${SAGA_PROJECT:-}"
feature="$(jq -r '.backend.feature // empty' "$file")"
feature="${feature:-${SAGA_FEATURE:-}}"
if [ -z "$project" ]; then
  echo "error: SAGA_PROJECT is not set: use 'bind qa = saga { env.SAGA_PROJECT = \"...\" }'" >&2
  exit 1
fi
if [ -z "$feature" ]; then
  echo "error: no saga feature: set backend.feature in the questions file" >&2
  exit 1
fi

# Saga's batch file is the question file without cloche's opaque backend settings.
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT
jq 'del(.backend)' "$file" > "$tmp"
want="$(jq '.questions | length' "$tmp")"

out="$("$SAGA_BIN" ask -p "$project" -f "$feature" --file "$tmp" --json)" || {
  echo "error: saga ask failed" >&2
  exit 1
}
ids="$(printf '%s' "$out" | jq -r '.questions[]? | if type == "object" then .id else . end' 2>/dev/null || true)"
got="$(printf '%s' "$ids" | grep -c . || true)"
if [ "$got" -ne "$want" ]; then
  echo "error: saga returned $got question ids for $want questions:" >&2
  printf '%s\n' "$out" >&2
  exit 1
fi
cloche set question_ids "$ids"
echo "Asked $got question(s) in $project/$feature"
