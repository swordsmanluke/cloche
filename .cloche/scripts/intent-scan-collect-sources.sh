#!/usr/bin/env bash
# intent-scan-collect-sources.sh — Gather docs, git log, and run/task-prompt
# material that changed since the last intent-scan (cursors persisted in
# .cloche/intent/scan-state.yaml), for the extract step to mine. Deterministic;
# no LLM. Emits CLOCHE_RESULT:none via `cloche intent collect-sources` when
# nothing is new, which the workflow wires straight to done.
set -euo pipefail

PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)

if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi

OUT="$TEMP/intent-scan-sources"

cloche intent collect-sources --project "$PROJECT_DIR" --out "$OUT"

cloche set intent_scan_sources_dir "$OUT"
