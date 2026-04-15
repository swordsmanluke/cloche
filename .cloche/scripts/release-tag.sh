#!/usr/bin/env bash
# release-tag.sh — Create an annotated git tag for the current version, using
# the top CHANGELOG.md entry as the tag message. The same notes are reused by
# release-publish.sh for the GitHub Release body.
set -euo pipefail

PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)

if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi

VERSION=$(cat "$PROJECT_DIR/internal/version/VERSION")
TAG="v${VERSION}"
NOTES="$TEMP/release_notes.md"

# Extract the body of the top changelog entry: everything from the first
# "## v<VERSION>" heading up to (but not including) the next "## v" heading
# or EOF. The heading line itself is stripped; only the body is used.
python3 - "$PROJECT_DIR/CHANGELOG.md" "$VERSION" "$NOTES" <<'PY'
import pathlib, re, sys

src = pathlib.Path(sys.argv[1]).read_text()
version = sys.argv[2]
out = pathlib.Path(sys.argv[3])

pattern = re.compile(
    r'^## v' + re.escape(version) + r'\b[^\n]*\n(.*?)(?=^## v|\Z)',
    flags=re.MULTILINE | re.DOTALL,
)
m = pattern.search(src)
if m is None:
    print(f"error: no '## v{version}' entry in CHANGELOG.md", file=sys.stderr)
    sys.exit(1)

out.write_text(m.group(1).strip() + "\n")
PY

git -C "$PROJECT_DIR" tag -a "$TAG" -F "$NOTES"

cloche set release_notes_path "$NOTES"
cloche set release_tag "$TAG"

echo "Created tag $TAG (notes in $NOTES)"
