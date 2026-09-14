#!/usr/bin/env python3
"""Print ready tasks from beads (bd) as JSONL for the cloche loop.

This is the task-tracker integration point. The loop runs this script and
expects zero or more JSON objects on stdout, one task per line:

    {"id": "...", "title": "...", "description": "...", "status": "open"}

Only tasks with status "open" are dispatched. To use a different tracker
(GitHub Issues, Jira, Linear, ...), replace this script with one that emits
the same JSONL.

A task is ready when:
  1. bd considers it ready (open, not blocked or deferred)
  2. every closed dependency has a succeeded cloche run
"""
import json
import shutil
import subprocess
import sys

if shutil.which("bd") is None:
    print("error: the beads CLI (bd) is not installed.", file=sys.stderr)
    print("Install it (https://github.com/steveyegge/beads), or swap in your own", file=sys.stderr)
    print("task tracker.", file=sys.stderr)
    sys.exit(1)

ready = subprocess.run(["bd", "ready", "--json"], capture_output=True, text=True)
if ready.returncode != 0:
    print(f"error: bd ready failed: {ready.stderr.strip()}", file=sys.stderr)
    sys.exit(1)

for task in json.loads(ready.stdout or "[]"):
    # bd ready lists open and in_progress tasks; in_progress means another run
    # already claimed it. Epics are containers for child tasks, not work.
    if task.get("status") != "open" or task.get("issue_type") == "epic":
        continue

    # A dependency closed in the tracker must also have a succeeded cloche
    # run, so work never starts from a base its prerequisite didn't land on.
    show = subprocess.run(["bd", "show", task["id"], "--json"], capture_output=True, text=True)
    closed_deps = []
    if show.returncode == 0:
        try:
            details = json.loads(show.stdout)[0]
            closed_deps = [d["id"] for d in details.get("dependencies") or []
                           if d.get("status") == "closed"]
        except (ValueError, LookupError):
            pass

    blocked = False
    for dep_id in closed_deps:
        # errors="replace": listing output may truncate task titles and must
        # never crash this gate on a stray byte.
        runs = subprocess.run(
            ["cloche", "list", "--all", "--issue", dep_id, "--state", "succeeded"],
            capture_output=True, text=True, errors="replace",
        )
        if "succeeded" not in runs.stdout:
            blocked = True
            break

    if not blocked:
        print(json.dumps({
            "id": task["id"],
            "title": task.get("title", ""),
            "description": task.get("description", ""),
            "status": "open",
        }))
