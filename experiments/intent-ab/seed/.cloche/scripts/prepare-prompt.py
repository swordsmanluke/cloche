#!/usr/bin/env python3
"""Build the agent's task prompt and pass it to the container via the KV store.

This is the host-to-container data handoff, the pattern to copy whenever a
step needs to send data to another step:

  1. Read the task's title and description from beads.
  2. Write the prompt to a file under temp_file_dir — a per-run directory the
     daemon creates and mounts into the container.
  3. Publish the file path with "cloche set task_prompt_path".

The implement prompt reads it back with "clo get task_prompt_path" inside the
container. KV values are capped at 1 KB, so anything bigger travels as a file
path, never as a value.
"""
import json
import os
import subprocess
import sys

task_id = os.environ.get("CLOCHE_TASK_ID", "")
if not task_id:
    print("error: CLOCHE_TASK_ID not set", file=sys.stderr)
    sys.exit(1)

show = subprocess.run(["bd", "show", task_id, "--json"], capture_output=True, text=True)
if show.returncode != 0:
    print(f"error: could not look up task {task_id}", file=sys.stderr)
    sys.exit(1)

task = json.loads(show.stdout)[0]
title = task.get("title", "")
if not title:
    print(f"error: task {task_id} has no title", file=sys.stderr)
    sys.exit(1)

prompt = f"## Task: {title}\n\n{task.get('description', '')}\n"

# temp_file_dir is project-relative (e.g. .cloche/runs/<run-id>), and the run
# directory is mounted into the container at the same relative path under
# /workspace — so the path stored below is valid on both sides.
temp_dir = subprocess.run(
    ["cloche", "get", "temp_file_dir"], check=True, capture_output=True, text=True,
).stdout.strip()
prompt_path = os.path.join(temp_dir, "task_prompt.md")
with open(prompt_path, "w") as f:
    f.write(prompt)

subprocess.run(["cloche", "set", "task_prompt_path", prompt_path], check=True)
print(f"Wrote prompt for {task_id} to {prompt_path}")
