#!/usr/bin/env bash
# lib.sh — shared helpers for saga-judge scripts. Source it; do not run it.
#
# SAGA_BIN overrides the saga binary (tests point it at a stub).
SAGA_BIN="${SAGA_BIN:-saga}"
# KV_BIN overrides the key/value tool: `cloche` on the host.
KV_BIN="${SAGA_JUDGE_KV_BIN:-cloche}"

kv_get() { "$KV_BIN" get "$1" 2>/dev/null || true; }
kv_set() { "$KV_BIN" set "$1" "$2"; }
