#!/usr/bin/env bash
# release-publish.sh — Push main + the release tag to origin and create a
# GitHub Release using the tag's message as the body. Respects
# CLOCHE_RELEASE_DRY_RUN=1 for rehearsals.
set -euo pipefail

PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TAG=$(cloche get release_tag)
NOTES=$(cloche get release_notes_path)

if [ -z "$TAG" ] || [ -z "$NOTES" ]; then
  echo "error: release_tag or release_notes_path missing — did release-tag run?" >&2
  exit 1
fi

if [ "${CLOCHE_RELEASE_DRY_RUN:-}" = "1" ]; then
  echo "dry run: would push origin main and $TAG, and create GitHub Release $TAG"
  echo "CLOCHE_RESULT:skipped"
  exit 0
fi

git -C "$PROJECT_DIR" push origin main
git -C "$PROJECT_DIR" push origin "$TAG"

gh release create "$TAG" --title "$TAG" --notes-file "$NOTES"

echo "Published $TAG"
