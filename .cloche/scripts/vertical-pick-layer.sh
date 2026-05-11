#!/usr/bin/env bash
# vertical-pick-layer.sh — select the next layer task to implement.
#
# Layer tasks are bead children of the parent feature task (set via
# `bd create --parent <feature-id>`). Among open layer children, we pick the
# topmost one whose dependencies are all closed.
#
# Inputs (KV via cloche get):
#   vertical_base_branch — base branch for the test plan (default "main")
#
# Inputs (env, set by daemon):
#   CLOCHE_TASK_ID — the parent feature task ID
#
# Outputs (KV via cloche set):
#   current_layer_id    — chosen layer task ID
#   current_base_branch — branch this layer should be based on (test-plan branch
#                         for L1, previous layer's branch for later layers)
#
# Wires:
#   has-layer        — found a layer ready to implement
#   no-more-layers   — every layer for this feature is closed
#   fail             — bead lookup failed or invariant broken
set -uo pipefail

if [ -z "${CLOCHE_TASK_ID:-}" ]; then
  echo "error: CLOCHE_TASK_ID not set (parent feature task)" >&2
  echo "CLOCHE_RESULT:fail"
  exit 0
fi

feature_id="$CLOCHE_TASK_ID"

# Find all child tasks of the feature, ordered by creation time (oldest first).
layers_json=$(bd list --parent "$feature_id" --all --json 2>/dev/null) || layers_json="[]"
if [ "$layers_json" = "[]" ] || [ -z "$layers_json" ]; then
  echo "error: feature $feature_id has no child layer tasks" >&2
  echo "CLOCHE_RESULT:fail"
  exit 0
fi

# Initial base: the test-plan branch (always present after Phase 1) sits between
# the user-specified vertical_base_branch and L1. record-test-plan sets
# test_plan_branch in KV after the test-plan PR is approved.
test_plan_branch=$(cloche get test_plan_branch 2>/dev/null || true)
if [ -n "$test_plan_branch" ]; then
  base="$test_plan_branch"
else
  # Test plan hasn't landed yet — should not happen, but fall back to user base.
  base=$(cloche get vertical_base_branch 2>/dev/null || echo "main")
fi

chosen=""
chosen_base=""

count=$(echo "$layers_json" | jq 'length')
for (( i=0; i<count; i++ )); do
  layer=$(echo "$layers_json" | jq -c ".[$i]")
  layer_id=$(echo "$layer" | jq -r '.id')
  layer_status=$(echo "$layer" | jq -r '.status')

  if [ "$layer_status" = "closed" ]; then
    # Already done; its branch becomes the base for the next pickable layer.
    base="vertical/${feature_id}/${layer_id}"
    continue
  fi

  if [ "$layer_status" != "open" ]; then
    # in_progress, blocked, deferred, etc. — not pickable
    continue
  fi

  # Check that all blocking dependencies are closed. We only consider
  # `dependency_type == "blocks"` — bd surfaces the parent-child relationship
  # as a dependency too (`dependency_type: "parent-child"`), and the parent
  # feature task is open for the entire vertical run, so including it would
  # block every layer indefinitely.
  open_deps=$(bd show "$layer_id" --json 2>/dev/null \
    | jq -r '.[0].dependencies[]? | select(.dependency_type == "blocks") | select(.status != "closed") | .id' 2>/dev/null) || open_deps=""

  if [ -n "$open_deps" ]; then
    continue
  fi

  chosen="$layer_id"
  chosen_base="$base"
  break
done

if [ -z "$chosen" ]; then
  echo "All layer tasks for $feature_id are closed."
  echo "CLOCHE_RESULT:no-more-layers"
  exit 0
fi

cloche set current_layer_id "$chosen"
cloche set current_base_branch "$chosen_base"

# The in-container read-layer step can't run `bd` (not in the agent image)
# and can't fit a 2-KB description into KV (1-KB cap). Stage the metadata
# as a file under temp_file_dir — that directory is bind-mounted into
# container steps, so the file is readable as-is on the container side.
chosen_json=$(bd show "$chosen" --json 2>/dev/null) || chosen_json=""
chosen_title=$(echo "$chosen_json" | jq -r '.[0].title // empty' 2>/dev/null)
chosen_desc=$(echo "$chosen_json" | jq -r '.[0].description // empty' 2>/dev/null)
cloche set current_layer_title "$chosen_title"

temp_file_dir=$(cloche get temp_file_dir 2>/dev/null || true)
if [ -z "$temp_file_dir" ]; then
  echo "error: temp_file_dir not set in KV (daemon should seed this at run start)" >&2
  echo "CLOCHE_RESULT:fail"
  exit 0
fi
layer_desc_path="${temp_file_dir}/layer-${chosen}.md"
mkdir -p "$temp_file_dir"
printf '%s\n' "$chosen_desc" > "$layer_desc_path"
cloche set current_layer_description_path "$layer_desc_path"

echo "Picked layer $chosen (base: $chosen_base, description -> $layer_desc_path)"
echo "CLOCHE_RESULT:has-layer"
