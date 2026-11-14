#!/usr/bin/env bash
# ask.sh — Contract: kv questions_file -> out question_ids (one id per line).
set -euo pipefail
exec 2>&1
trap 'echo "error: ${BASH_SOURCE[0]} line $LINENO: $BASH_COMMAND"' ERR
file="$(cloche get questions_file)"
n="$(grep -o '"prompt"' "$file" | wc -l)"
[ "$n" -gt 0 ] || { echo "error: no questions in $file"; exit 1; }
ids=""
for _ in $(seq "$n"); do
  id="$(qa-stub create)"
  ids="${ids:+$ids
}$id"
done
cloche set question_ids "$ids"
