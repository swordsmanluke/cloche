#!/usr/bin/env bash
# verify-changes.sh — Fail if the implement step made no code changes.
# Inside the container, the project is copied with its git history intact.
# The implement step should have committed changes on top of HEAD.
# We check for uncommitted changes OR new commits with actual diffs.
set -euo pipefail

# Check for uncommitted changes (staged or unstaged)
if ! git diff --quiet HEAD 2>/dev/null || ! git diff --cached --quiet 2>/dev/null; then
  echo "changes detected (uncommitted changes present)"
  exit 0
fi

# Check if the most recent commit was made by the agent (not the initial state).
# The agent uses "Co-Authored-By: Claude" or similar markers.
# Simpler: check if any files changed in the last commit vs its parent.
if git rev-parse HEAD~1 >/dev/null 2>&1; then
  if ! git diff --quiet HEAD~1 HEAD 2>/dev/null; then
    CHANGED=$(git diff --stat HEAD~1 HEAD | tail -1)
    echo "changes verified: $CHANGED"
    exit 0
  fi
fi

echo "error: implement step made no code changes" >&2
exit 1
