#!/usr/bin/env bash
# check-main-schedule.sh — set the target timestamp for the next check.
# CLOCHE_CHECK_MAIN_INTERVAL is in seconds (default 21600 = 6h).
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/check-main-state.sh"

interval="${CLOCHE_CHECK_MAIN_INTERVAL:-21600}"
next=$(( $(date +%s) + interval ))
state_put next_at "$next"
echo "next check-main at $(date -u -d "@$next" +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || echo "$next")"
