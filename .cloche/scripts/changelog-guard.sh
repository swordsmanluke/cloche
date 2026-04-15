#!/usr/bin/env bash
# changelog-guard.sh — Preconditions for drafting a changelog.
# Runs before commit collection so the user gets a fast, clear failure if the
# tree or branch state is wrong.
set -euo pipefail

PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"

if ! git -C "$PROJECT_DIR" diff-index --quiet HEAD -- 2>/dev/null || \
   [ -n "$(git -C "$PROJECT_DIR" status --porcelain)" ]; then
  echo "error: working tree has uncommitted changes — commit or stash before generating a changelog" >&2
  exit 1
fi

branch=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD)
if [ "$branch" != "main" ]; then
  echo "error: must be on 'main' (currently on '$branch')" >&2
  exit 1
fi

if [ -z "$(git -C "$PROJECT_DIR" tag -l 'v*')" ]; then
  cat >&2 <<'EOF'
error: no 'v*' tags found — bootstrap the release process first.

Run once, by hand:
  git fetch origin
  BOOT_SHA=$(git rev-parse origin/main)
  BOOT_VERSION=$(git show "$BOOT_SHA:internal/version/VERSION")
  git tag -a "v$BOOT_VERSION" -m "Bootstrap tag" "$BOOT_SHA"
  git push origin "v$BOOT_VERSION"
  gh release create "v$BOOT_VERSION" --title "v$BOOT_VERSION" \
    --notes "Bootstrap release. Changelog entries begin with the next release."
EOF
  exit 1
fi

echo "ok: on main, tree clean, v* tag present"
