#!/usr/bin/env python3
"""Close the task in beads after a successful run.

This step is wired on the success path (merge:success -> close-task), so
reaching it means the work landed on the base branch.
"""
import os
import subprocess
import sys

task_id = os.environ.get("CLOCHE_TASK_ID", "")
if not task_id:
    print("warning: CLOCHE_TASK_ID not set, skipping", file=sys.stderr)
    sys.exit(0)

if subprocess.run(["bd", "close", task_id], capture_output=True).returncode != 0:
    print(f"warning: could not close task {task_id}", file=sys.stderr)
else:
    print(f"Closed task {task_id}")
