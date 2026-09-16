#!/usr/bin/env python3
"""Reset the task to open in beads and stop the orchestration loop.

This is the emergency brake — it halts all automated work so a human
can investigate what went wrong.
"""
import os
import subprocess

task_id = os.environ.get("CLOCHE_TASK_ID", "")
if task_id:
    if subprocess.run(["bd", "update", task_id, "-s", "open"]).returncode == 0:
        print(f"Unclaimed task {task_id}")
    else:
        print(f"warning: could not reset task {task_id} to open")

# Pilot mode: keep the loop running so the arm driver's budget caps
# (attempts / wall clock) do the stopping, not per-failure human review.
print("Task unclaimed; loop left running (pilot mode)")
