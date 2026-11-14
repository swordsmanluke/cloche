#!/usr/bin/env bash
# read.sh — Contract: kv question_id -> out answer_path. Exits non-zero when
# there is no answer. A human answer wins over a judge answer.
set -euo pipefail
exec 2>&1
trap 'echo "error: ${BASH_SOURCE[0]} line $LINENO: $BASH_COMMAND"' ERR
id="$(cloche get question_id)"
path="$(qa-stub answer-path "$id")"
cloche set answer_path "$path"
