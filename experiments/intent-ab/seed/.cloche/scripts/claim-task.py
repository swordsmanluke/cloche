#!/usr/bin/env python3
"""Claim the daemon-assigned task by marking it in_progress in beads.

The daemon sets CLOCHE_TASK_ID in the environment of every step in this run.
This step only updates tracker state — the task's content reaches the coding
agent via the KV store (see prepare-prompt.py), not via stdout.
"""
import os
import subprocess
import sys

task_id = os.environ.get("CLOCHE_TASK_ID", "")
if not task_id:
    print("error: CLOCHE_TASK_ID not set (is the loop running?)", file=sys.stderr)
    sys.exit(1)

if subprocess.run(["bd", "update", task_id, "-s", "in_progress"]).returncode != 0:
    print(f"error: could not claim task {task_id}", file=sys.stderr)
    sys.exit(1)

print(f"Claimed task {task_id}")
