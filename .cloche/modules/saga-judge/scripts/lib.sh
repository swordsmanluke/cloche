#!/usr/bin/env bash
# lib.sh — shared helpers for saga-judge scripts. Source it; do not run it.
#
# SAGA_BIN overrides the saga binary (tests point it at a stub).
SAGA_BIN="${SAGA_BIN:-saga}"
# KV_BIN overrides the key/value tool: `cloche` on the host.
KV_BIN="${SAGA_JUDGE_KV_BIN:-cloche}"

kv_get() { "$KV_BIN" get "$1" 2>/dev/null || true; }
kv_set() { "$KV_BIN" set "$1" "$2"; }

# saga_poll_json <batch> — one non-blocking `saga poll` with --json. Sets POLL_JSON.
# Exit 2 from saga means "still open" and is not an error; exit 0 means every
# question is answered; anything else (exit 1, spawn failure) is a failure.
saga_poll_json() {
  local rc=0
  POLL_JSON=$("$SAGA_BIN" poll "$1" --no-wait --json) || rc=$?
  case "$rc" in
    0 | 2) return 0 ;;
    *) echo "error: saga poll failed (exit $rc)" >&2; return 1 ;;
  esac
}
