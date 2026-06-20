#!/usr/bin/env bash
# vertical-finalize.sh — squash-merge the entire stack into the user-specified base.
#
# Strategy B: every PR (design, test-plan, each layer, docs) is left approved-but-open
# during the run. At finalize, we squash-merge the docs branch (which sits on top
# of the entire stack) into vertical_base_branch. That single squash captures every
# commit in the stack — design + test plan + all layers + docs — as one commit.
#
# Stack walk order: design → test-plan → layers → docs.
# If vertical/<feat>/design exists on origin, it is acknowledged first.
#
# Reads (KV):
#   vertical_base_branch — target base (default "main")
#   feature task ID via CLOCHE_TASK_ID
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/agent-creds.sh"

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

# Build the ordered stack list for the commit message (design first if present).
declare -a stack_parts=()
design_branch="vertical/${feature_id}/design"
if git ls-remote --exit-code --heads origin "$design_branch" >/dev/null 2>&1; then
  stack_parts+=("Design doc")
  echo "Stack includes design branch: $design_branch (merged first)"
fi
test_plan_branch="vertical/${feature_id}/test-plan"
if git ls-remote --exit-code --heads origin "$test_plan_branch" >/dev/null 2>&1; then
  stack_parts+=("BDD test plan (Gherkin scenarios)")
fi
layer_count=$(bd list --parent "$feature_id" --all --json 2>/dev/null | jq -r 'length' || echo "0")
stack_parts+=("${layer_count} implementation layer(s)")
stack_parts+=("Documentation updates")

# Bring base up to date locally.
git fetch origin "$base":"$base" 2>/dev/null || git fetch origin "$base"
git checkout "$base"
git reset --hard "origin/$base"

# Squash-merge the entire stack (everything between $base and docs branch) into base.
# The docs branch sits on top of the full stack (design → test-plan → layers → docs),
# so squashing it captures every commit including the design phase.
git fetch origin "$docs_branch"
git merge --squash "origin/$docs_branch"

# Build a commit message summarizing the feature.
title=$(bd show "$feature_id" --json 2>/dev/null | jq -r '.[0].title // empty')
if [ -z "$title" ]; then
  title="Feature $feature_id"
fi

# Format stack summary lines.
stack_lines=""
for part in "${stack_parts[@]}"; do
  stack_lines+="- $part"$'\n'
done

git commit -m "$(cat <<EOF
$title

Implements feature $feature_id via the vertical workflow:
${stack_lines}EOF
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
cloche set design_branch ""
cloche set vertical_base_branch ""
