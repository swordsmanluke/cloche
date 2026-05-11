#!/usr/bin/env bash
# vertical-extract.sh — helpers for host-side push of sub-workflow output.
#
# Sub-workflow containers commit to a human-friendly branch (e.g.
# `vertical/<feature>/test-plan`), but the daemon's extraction step replays
# those commits onto its own pre-allocated branch (`cloche/<runID>-_default`)
# in the host repo. The pointer is exposed via the `child_branch` KV key.
#
# These helpers translate that back so host scripts can push under the name
# the workflow design expects.

# rename_extracted_to <expected-branch>
#
# If the daemon-extracted branch (read from `child_branch` KV) exists locally
# and differs from <expected-branch>, force-update <expected-branch> to point
# at the same commit. After this, any `git push origin <expected-branch>`
# uploads the sub-workflow's commits.
rename_extracted_to() {
  local expected="$1"
  local extracted
  extracted=$(cloche get child_branch 2>/dev/null || true)
  if [ -z "$extracted" ] || [ "$extracted" = "$expected" ]; then
    return 0
  fi
  if ! git rev-parse --verify --quiet "$extracted" >/dev/null; then
    echo "warning: child_branch=$extracted not present locally; expected branch $expected may already hold the work" >&2
    return 0
  fi
  git branch -f "$expected" "$extracted"
  echo "Renamed extracted branch $extracted -> $expected"
}
