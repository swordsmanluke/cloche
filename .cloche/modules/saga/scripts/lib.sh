#!/usr/bin/env bash
# lib.sh — shared helpers for the saga qa backend. Source it; do not run it.
# SAGA_BIN overrides the saga binary (tests point it at a stub).
SAGA_BIN="${SAGA_BIN:-saga}"

# saga_show <question-id> — `saga show <id> --json` on stdout. Real Saga's JSON:
# {"id","status":{"state"},"answers":[{"author":"human|agent","select":[1],
# "status":null,"text"}]} — author is a plain string and an answer's status may
# be null. (read.sh also tolerates an object author carrying a .kind field.)
saga_show() { "$SAGA_BIN" show "$1" --json; }
