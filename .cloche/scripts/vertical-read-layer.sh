#!/usr/bin/env bash
# vertical-read-layer.sh — prepare the per-layer prompt context for the implement step.
#
# Runs in-container. Reads:
#   current_layer_id    — KV (set by host's pick-next-layer)
#   current_base_branch — KV
# Writes:
#   layer_prompt_path   — KV; path to the markdown the agent should consume
#   layer_branch        — KV; the branch this layer should commit to
set -euo pipefail

layer_id=$(clo get current_layer_id 2>/dev/null || true)
base_branch=$(clo get current_base_branch 2>/dev/null || echo "main")
feature_id="${CLOCHE_TASK_ID:-unknown}"

if [ -z "$layer_id" ]; then
  echo "error: current_layer_id not set in KV" >&2
  exit 1
fi

# Layer title/description are pre-staged in KV by the host's
# vertical-pick-layer.sh step. We can't call `bd` directly here — the agent
# image doesn't ship the bd CLI, and the host's bead db is on a unix socket
# that doesn't reach inside the container.
layer_title=$(clo get current_layer_title 2>/dev/null || true)
layer_body=$(clo get current_layer_description 2>/dev/null || true)
if [ -z "$layer_title" ]; then
  echo "error: current_layer_title not in KV — did pick-next-layer run?" >&2
  exit 1
fi

# Make the layer branch off the base.
layer_branch="vertical/${feature_id}/${layer_id}"
git fetch origin "$base_branch":"$base_branch" 2>/dev/null || true
git checkout -B "$layer_branch" "origin/$base_branch" 2>/dev/null \
  || git checkout -B "$layer_branch" "$base_branch"

# Compose the prompt context the agent will read.
prompt_dir="$(clo get temp_file_dir 2>/dev/null || echo /workspace/.cloche/runs/${CLOCHE_RUN_ID})"
mkdir -p "$prompt_dir"
prompt_path="$prompt_dir/layer-context.md"

cat > "$prompt_path" <<EOF
# Layer to implement

**Layer task ID:** $layer_id
**Parent feature task:** $feature_id
**Layer branch:** $layer_branch  (already checked out)
**Base branch:** $base_branch

## Layer task title
$layer_title

## Layer task description
$layer_body
EOF

clo set layer_prompt_path "$prompt_path"
clo set layer_branch "$layer_branch"
echo "Prepared layer $layer_id on branch $layer_branch (base: $base_branch)"
