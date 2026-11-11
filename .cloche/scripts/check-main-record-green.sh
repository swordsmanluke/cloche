#!/usr/bin/env bash
# check-main-record-green.sh — the suite passed on main's HEAD.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/check-main-state.sh"

head=$(main_head)
state_put last_checked_sha "$head"
state_put last_green_sha "$head"
echo "main is green at ${head:0:12}"
