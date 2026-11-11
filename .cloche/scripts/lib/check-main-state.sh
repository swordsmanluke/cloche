#!/usr/bin/env bash
# check-main-state.sh — shared state for the check-main workflow scripts.
# Source it; it defines PROJECT_DIR, STATE_DIR and small get/put helpers.
# State is plain files under .cloche/runs/ (gitignored runtime state) so it
# survives across runs of the workflow:
#   next_at           unix time the next check is due
#   last_checked_sha  main's HEAD at the last check (green, or red and signalled)
#   last_green_sha    main's HEAD at the last green check
PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
STATE_DIR="${CLOCHE_CHECK_MAIN_STATE_DIR:-$PROJECT_DIR/.cloche/runs/_check-main}"
mkdir -p "$STATE_DIR"

state_get() { cat "$STATE_DIR/$1" 2>/dev/null || true; }
state_put() { printf '%s\n' "$2" > "$STATE_DIR/$1"; }
main_head() { git -C "$PROJECT_DIR" rev-parse --verify -q refs/heads/main; }
