#!/usr/bin/env bash
# changelog-collect-commits.sh — Gather the set of commits since the last
# v* tag, filter out per-task version bumps and auto-generated run logs,
# flag commits that touch the user-visible surface, and write patches for
# the agent to consume.
set -euo pipefail

PROJECT_DIR="${CLOCHE_PROJECT_DIR:-.}"
TEMP=$(cloche get temp_file_dir)

if [ -z "$TEMP" ]; then
  echo "error: temp_file_dir not set in KV store" >&2
  exit 1
fi

mkdir -p "$TEMP/diffs"

LAST_TAG=$(git -C "$PROJECT_DIR" tag -l 'v*' | sort -V | tail -1)
if [ -z "$LAST_TAG" ]; then
  echo "error: no v* tag found (should have been caught by guard)" >&2
  exit 1
fi

# Breaking-change watchlist: commits touching these paths get a '!' marker
# so the agent scrutinizes them for user-visible breakage.
WATCHLIST_REGEX='^(internal/dsl/|internal/protocol/|docs/workflows\.md$|cmd/cloche/|cmd/clo/|\.cloche/scripts/|\.cloche/prompts/|\.cloche/host\.cloche$|\.cloche/Dockerfile$)'

raw_list=$(git -C "$PROJECT_DIR" log --pretty=format:'%H%x09%s' "$LAST_TAG..HEAD")

: > "$TEMP/commits.txt"
kept=0
noise=0

# Only true noise: per-task "Version X.Y.Z" bumps, which touch only
# internal/version/VERSION. Commits with subjects like "cloche run
# <id>-develop: develop (succeeded)" are squash commits from the develop
# workflow — their subjects are uninformative but their diffs contain the
# actual feature work, so we keep them and let the agent summarize via the
# diff.
version_re='^Version [0-9]+\.[0-9]+\.[0-9]+$'

while IFS=$'\t' read -r sha subject; do
  [ -z "$sha" ] && continue

  if [[ "$subject" =~ $version_re ]]; then
    noise=$((noise + 1))
    continue
  fi

  marker=""
  if git -C "$PROJECT_DIR" show --name-only --pretty=format: "$sha" \
       | grep -Eq "$WATCHLIST_REGEX"; then
    marker=" !"
  fi

  short=$(git -C "$PROJECT_DIR" rev-parse --short=7 "$sha")
  printf '%s\t%s%s\n' "$sha" "$subject" "$marker" >> "$TEMP/commits.txt"
  git -C "$PROJECT_DIR" show --stat --patch "$sha" > "$TEMP/diffs/$short.patch"
  kept=$((kept + 1))
done <<< "$raw_list"

if [ "$kept" -eq 0 ]; then
  echo "error: nothing to release since $LAST_TAG ($noise noise commits elided)" >&2
  exit 1
fi

cat internal/version/VERSION > "$TEMP/release_version.txt"
printf '%s\n' "$LAST_TAG" > "$TEMP/last_tag.txt"

cloche set release_temp_dir "$TEMP"

echo "Collected $kept commits since $LAST_TAG (elided $noise noise commits)"
echo "Artifacts: $TEMP"
