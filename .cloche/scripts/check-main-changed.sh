#!/usr/bin/env bash
# check-main-changed.sh — decide whether main needs checking.
#   changed   — main's HEAD differs from the last check: run the suite
#   unchanged — HEAD hasn't moved since the last check: skip
#   skip      — the project root isn't a clean checkout of main, so the
#               container's copy wouldn't be main's HEAD: skip this round
# Nothing here pauses anything; every branch goes back to the timer.
set -uo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/check-main-state.sh"

head=$(main_head) || { echo "error: no main branch in $PROJECT_DIR" >&2; exit 1; }
branch=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD)
if [ "$branch" != "main" ] || [ -n "$(git -C "$PROJECT_DIR" status --porcelain)" ]; then
  echo "project root is not a clean checkout of main (on '$branch'); skipping this check"
  echo "CLOCHE_RESULT:skip"
  exit 0
fi
if [ "$head" = "$(state_get last_checked_sha)" ]; then
  echo "main is still at ${head:0:12}; nothing new to check"
  echo "CLOCHE_RESULT:unchanged"
  exit 0
fi
echo "main moved to ${head:0:12}; running the suite"
echo "CLOCHE_RESULT:changed"
