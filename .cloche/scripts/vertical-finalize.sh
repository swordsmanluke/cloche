#!/usr/bin/env bash
# vertical-finalize.sh — squash-merge the entire stack into the user-specified base.
#
# Strategy B: every PR (test-plan, each layer, docs) is left approved-but-open
# during the run. At finalize, we squash-merge the docs branch (which sits on top
# of the entire stack) into vertical_base_branch. That single squash captures
# every commit in the stack — test plan + all layers + docs — as one commit.
#
# Reads (KV):
#   vertical_base_branch — target base (default "main")
#   feature task ID via CLOCHE_TASK_ID
set -euo pipefail

feature_id="${CLOCHE_TASK_ID:-}"
if [ -z "$feature_id" ]; then
  echo "error: CLOCHE_TASK_ID not set" >&2
  exit 1
fi

base=$(cloche get vertical_base_branch 2>/dev/null || echo "main")
docs_branch="vertical/${feature_id}/docs"

# Sanity: docs branch must exist on origin.
if ! git ls-remote --exit-code --heads origin "$docs_branch" >/dev/null 2>&1; then
  echo "error: docs branch $docs_branch not found on origin" >&2
  exit 1
fi

# Bring base up to date locally.
git fetch origin "$base":"$base" 2>/dev/null || git fetch origin "$base"
git checkout "$base"
git reset --hard "origin/$base"

# Squash-merge the entire stack (everything between $base and docs branch) into base.
git fetch origin "$docs_branch"
git merge --squash "origin/$docs_branch"

# Build a commit message summarizing the feature.
title=$(bd show "$feature_id" --json 2>/dev/null | jq -r '.[0].title // empty')
if [ -z "$title" ]; then
  title="Feature $feature_id"
fi

git commit -m "$(cat <<EOF
$title

Implements feature $feature_id via the vertical workflow:
- BDD test plan (Gherkin scenarios)
- $(echo "$(bd list --parent "$feature_id" --all --json 2>/dev/null | jq -r 'length')") implementation layers
- Documentation updates
EOF
)"

git push origin "$base"
echo "Squash-merged stack into $base for feature $feature_id"

# Close the parent feature task.
if bd close "$feature_id" 2>/dev/null; then
  echo "Closed feature task $feature_id"
else
  echo "warning: could not close feature task $feature_id" >&2
fi

# Clear vertical-run-scoped KV.
cloche set test_plan_branch ""
cloche set vertical_base_branch ""
