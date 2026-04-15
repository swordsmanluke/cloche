#!/usr/bin/env bash
# changelog-commit.sh — Prepend the reviewed drafts to CHANGELOG.md and
# docs/CHANGELOG-DETAILED.md, then commit. Idempotent per version: if the top
# entry already targets the current version, replace it instead of stacking.
set -euo pipefail

PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get release_temp_dir)

if [ -z "$TEMP" ]; then
  echo "error: release_temp_dir not set — was collect-commits skipped?" >&2
  exit 1
fi

VERSION=$(cat "$PROJECT_DIR/internal/version/VERSION")
DATE=$(date -u +%Y-%m-%d)

apply_draft() {
  local target="$1" draft="$2" header="$3"
  local heading="## v${VERSION} — ${DATE}"

  if [ ! -f "$target" ]; then
    printf '# %s\n\n' "$header" > "$target"
  fi

  python3 - "$target" "$draft" "$heading" "$VERSION" <<'PY'
import re, sys, pathlib

target_path = pathlib.Path(sys.argv[1])
draft_path = pathlib.Path(sys.argv[2])
heading = sys.argv[3]
version = sys.argv[4]

text = target_path.read_text()
draft = draft_path.read_text().strip() + "\n"

# Split the file into (header, entries). Header is everything up to the first
# top-level "## v" heading, or the whole file if none exists yet.
m = re.search(r'^## v', text, flags=re.MULTILINE)
if m is None:
    header = text.rstrip() + "\n\n"
    rest = ""
else:
    header = text[: m.start()]
    rest = text[m.start() :]

# If the top existing entry is for the same version, drop it (replace).
top_pattern = re.compile(
    r'^## v' + re.escape(version) + r'\b[^\n]*\n.*?(?=^## v|\Z)',
    flags=re.MULTILINE | re.DOTALL,
)
rest_new, n = top_pattern.subn('', rest, count=1)
replaced = n > 0

new_entry = f"{heading}\n\n{draft}\n"
target_path.write_text(header + new_entry + rest_new)
print("replaced" if replaced else "prepended")
PY
}

summary_target="$PROJECT_DIR/CHANGELOG.md"
detailed_target="$PROJECT_DIR/docs/CHANGELOG-DETAILED.md"

mkdir -p "$PROJECT_DIR/docs"

apply_draft "$summary_target"  "$TEMP/CHANGELOG-summary.draft.md"  "Cloche Changelog"
apply_draft "$detailed_target" "$TEMP/CHANGELOG-detailed.draft.md" "Cloche Detailed Changelog"

git -C "$PROJECT_DIR" add CHANGELOG.md docs/CHANGELOG-DETAILED.md
git -C "$PROJECT_DIR" commit -m "Changelog for v${VERSION}"

echo "Committed changelog for v${VERSION}"
