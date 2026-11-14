#!/usr/bin/env bash
# await.sh — Contract: kv question_ids -> out questions_open. Non-blocking:
# lists the ids that have no answer yet (empty when all are answered).
set -euo pipefail
exec 2>&1
trap 'echo "error: ${BASH_SOURCE[0]} line $LINENO: $BASH_COMMAND"' ERR
open=""
for id in $(cloche get question_ids); do
  if ! qa-stub answered "$id"; then
    open="${open:+$open
}$id"
  fi
done
cloche set questions_open "$open"
