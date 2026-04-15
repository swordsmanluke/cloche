#!/usr/bin/env bash
# release-guard.sh — Preconditions for tagging and publishing a release.
# Fails fast if the repo isn't in a shape where we can safely create a tag.
set -euo pipefail

PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"

if ! git -C "$PROJECT_DIR" diff-index --quiet HEAD -- 2>/dev/null || \
   [ -n "$(git -C "$PROJECT_DIR" status --porcelain)" ]; then
  echo "error: working tree has uncommitted changes" >&2
  exit 1
fi

branch=$(git -C "$PROJECT_DIR" rev-parse --abbrev-ref HEAD)
if [ "$branch" != "main" ]; then
  echo "error: must be on 'main' (currently on '$branch')" >&2
  exit 1
fi

VERSION=$(cat "$PROJECT_DIR/internal/version/VERSION")
TAG="v${VERSION}"

if git -C "$PROJECT_DIR" rev-parse "$TAG" >/dev/null 2>&1; then
  echo "error: tag $TAG already exists locally — bump version before re-releasing" >&2
  exit 1
fi

# Check remote — fetch first so we catch tags pushed elsewhere.
git -C "$PROJECT_DIR" fetch --tags --quiet origin 2>/dev/null || true
if git -C "$PROJECT_DIR" ls-remote --tags origin "refs/tags/$TAG" 2>/dev/null | grep -q .; then
  echo "error: tag $TAG already exists on origin" >&2
  exit 1
fi

# Changelog must have a top entry matching the current version.
changelog="$PROJECT_DIR/CHANGELOG.md"
if [ ! -f "$changelog" ]; then
  echo "error: CHANGELOG.md not found — run 'cloche run --workflow changelog' first" >&2
  exit 1
fi
top_entry=$(grep -m1 '^## v' "$changelog" || true)
if [ -z "$top_entry" ]; then
  echo "error: CHANGELOG.md has no '## v...' entry — run 'cloche run --workflow changelog' first" >&2
  exit 1
fi
if ! printf '%s' "$top_entry" | grep -q "^## v${VERSION}[[:space:]]"; then
  echo "error: top CHANGELOG.md entry is '$top_entry' but current version is v${VERSION}" >&2
  echo "       run 'cloche run --workflow changelog' to refresh the entry" >&2
  exit 1
fi

if [ "${CLOCHE_RELEASE_DRY_RUN:-}" != "1" ]; then
  if ! gh auth status >/dev/null 2>&1; then
    echo "error: 'gh' is not authenticated (set CLOCHE_RELEASE_DRY_RUN=1 to skip publish)" >&2
    exit 1
  fi
fi

echo "ok: ready to tag v${VERSION}"
