#!/usr/bin/env bash
# lib.sh — shared helpers for the saga qa backend. Source it; do not run it.
# SAGA_BIN overrides the saga binary (tests point it at a stub).
SAGA_BIN="${SAGA_BIN:-saga}"

# saga_show <question-id> — `saga show <id> --json` on stdout. Saga's JSON:
# {"id","status":{"state"},"answers":[{"author":{"kind":"human|judge"},"text"}]}
saga_show() { "$SAGA_BIN" show "$1" --json; }
