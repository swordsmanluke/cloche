#!/usr/bin/env python3
"""Remove the worktree and branch left over from the develop run.

The daemon publishes the branch name in the KV store as child_branch when it
pre-creates the extraction worktree; the worktree lives at
.gitworktrees/cloche/<branch-suffix>. Everything here is best-effort — a
successful merge already removed the worktree and branch.
"""
import os
import subprocess

project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")


def kv_get(key):
    result = subprocess.run(["cloche", "get", key], capture_output=True, text=True)
    return result.stdout.strip() if result.returncode == 0 else ""


branch = kv_get("child_branch")
if not branch:
    run_id = kv_get("child_run_id")
    if not run_id:
        print("warning: neither child_branch nor child_run_id found, skipping cleanup")
        raise SystemExit(0)
    branch = f"cloche/{run_id}"

suffix = branch.removeprefix("cloche/")
worktree_dir = os.path.join(project_dir, ".gitworktrees", "cloche", suffix)

if os.path.isdir(worktree_dir):
    subprocess.run(
        ["git", "-C", project_dir, "worktree", "remove", "--force", worktree_dir],
        capture_output=True,
    )

subprocess.run(["git", "-C", project_dir, "worktree", "prune"], capture_output=True)
subprocess.run(["git", "-C", project_dir, "branch", "-D", branch], capture_output=True)

print(f"Cleaned up {branch}")
