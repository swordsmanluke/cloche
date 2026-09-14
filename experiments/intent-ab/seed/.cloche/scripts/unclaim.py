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

# Stop the loop — a human must investigate and restart
subprocess.run(["cloche", "loop", "stop"])
print("Loop stopped — investigate and run 'cloche loop' when ready")
