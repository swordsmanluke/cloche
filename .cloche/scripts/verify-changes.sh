#!/usr/bin/env bash
# verify-changes.sh — Verify the previous step made code changes AND that the
# resulting tree still compiles.
#
# Inside the container, the project is copied with its git history intact.
# The previous step should have committed changes on top of HEAD. We check
# for uncommitted changes OR new commits with actual diffs, then run
# `go build ./...` so the workflow fails fast on broken commits.
set -euo pipefail

changes_detected=0

# Check for uncommitted changes (staged or unstaged)
if ! git diff --quiet HEAD 2>/dev/null || ! git diff --cached --quiet 2>/dev/null; then
  echo "changes detected (uncommitted changes present)"
  changes_detected=1
fi

# Check if the most recent commit added a diff over its parent.
if [ "$changes_detected" -eq 0 ] && git rev-parse HEAD~1 >/dev/null 2>&1; then
  if ! git diff --quiet HEAD~1 HEAD 2>/dev/null; then
    CHANGED=$(git diff --stat HEAD~1 HEAD | tail -1)
    echo "changes verified: $CHANGED"
    changes_detected=1
  fi
fi

if [ "$changes_detected" -eq 0 ]; then
  echo "error: previous step made no code changes" >&2
  exit 1
fi

# Compile check: a previously-broken vertical run committed code that did not
# build because nothing in the workflow ran the compiler. Gate the verify step
# on `go build` so future runs fail fast.
echo "running go build ./..."
if ! go build ./...; then
  echo "error: go build ./... failed after the previous step's changes" >&2
  exit 1
fi
