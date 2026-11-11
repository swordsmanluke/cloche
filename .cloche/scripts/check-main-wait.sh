#!/usr/bin/env bash
# check-main-wait.sh — poll script for the check-main workflow: the timer.
# Cloche has no time trigger (CLOCHE-A114), so the timer is a poll step that
# compares the current time with a target timestamp written by
# check-main-schedule.sh. No target yet (first run) means due immediately.
# Exit 0 with no marker = not yet; the poll step tries again after its interval.
set -uo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/check-main-state.sh"

next=$(state_get next_at)
now=$(date +%s)
if [ -z "$next" ] || [ "$now" -ge "$next" ]; then
  echo "check-main is due"
  echo "CLOCHE_RESULT:due"
fi
exit 0
