#!/usr/bin/env python3
"""Rebase the daemon-created result branch onto the base branch and fast-forward.

Before the develop sub-workflow ran, the daemon already created a branch and a
worktree for its results (.gitworktrees/cloche/<suffix>), extracted the
container's changes into it, and published the branch name in the KV store as
child_branch. This script only consumes that worktree — it never creates one.

On a rebase conflict the worktree is preserved and worktree_path/base_branch
(set below) feed the fix-merge agent prompt via template variables; after
fix-merge completes the rebase, this script re-runs and the rebase is a no-op.
"""
import os
import subprocess
import sys


def kv_get(key):
    result = subprocess.run(["cloche", "get", key], capture_output=True, text=True)
    return result.stdout.strip() if result.returncode == 0 else ""


def git(args, cwd, **kwargs):
    env = dict(os.environ)
    name = env.get("CLOCHE_GIT_AUTHOR_NAME") or "cloche"
    email = env.get("CLOCHE_GIT_AUTHOR_EMAIL") or "cloche@local"
    env.update({"GIT_AUTHOR_NAME": name, "GIT_AUTHOR_EMAIL": email,
                "GIT_COMMITTER_NAME": name, "GIT_COMMITTER_EMAIL": email})
    return subprocess.run(["git", "-C", cwd] + args, env=env, **kwargs)


project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")

branch = kv_get("child_branch")
if not branch:
    run_id = kv_get("child_run_id")
    if not run_id:
        print("error: neither child_branch nor child_run_id found in run context", file=sys.stderr)
        sys.exit(1)
    branch = f"cloche/{run_id}"

# The daemon puts the worktree at .gitworktrees/cloche/<branch-suffix>.
suffix = branch.removeprefix("cloche/")
worktree_dir = os.path.join(project_dir, ".gitworktrees", "cloche", suffix)

if git(["rev-parse", "--verify", branch], project_dir, capture_output=True).returncode != 0:
    print(f"error: branch {branch} does not exist", file=sys.stderr)
    sys.exit(1)

if not os.path.isdir(worktree_dir):
    print(f"error: worktree {worktree_dir} does not exist (the daemon pre-creates it)", file=sys.stderr)
    sys.exit(1)

base_branch = git(["rev-parse", "--abbrev-ref", "HEAD"], project_dir,
                  check=True, capture_output=True, text=True).stdout.strip()

# Publish state for the fix-merge step, which reads these as {{ $worktree_path }}
# and {{ $base_branch }} in its prompt template.
subprocess.run(["cloche", "set", "worktree_path", worktree_dir], check=True)
subprocess.run(["cloche", "set", "base_branch", base_branch], check=True)

# Stash untracked files in the main tree so the fast-forward can't collide.
stash_msg = f"cloche/merge: {branch}"
git(["stash", "--include-untracked", "-m", stash_msg], project_dir, capture_output=True)
stashed = stash_msg in git(["stash", "list"], project_dir,
                           capture_output=True, text=True).stdout

if git(["rebase", base_branch], worktree_dir).returncode != 0:
    git(["rebase", "--abort"], worktree_dir, capture_output=True)
    if stashed:
        git(["stash", "pop"], project_dir)
    print(f"error: rebase failed — worktree preserved at {worktree_dir}", file=sys.stderr)
    sys.exit(1)

rebased_head = git(["rev-parse", "HEAD"], worktree_dir,
                   check=True, capture_output=True, text=True).stdout.strip()

# Remove the worktree before merging (git refuses to delete a checked-out
# branch), update the ref, and fast-forward the base branch.
git(["worktree", "remove", "--force", worktree_dir], project_dir, capture_output=True)
git(["update-ref", f"refs/heads/{branch}", rebased_head], project_dir, check=True)
git(["merge", "--ff-only", branch], project_dir, check=True)

if stashed:
    git(["stash", "pop"], project_dir)

git(["branch", "-D", branch], project_dir, capture_output=True)
print(f"Merged {branch} into {base_branch} ({rebased_head[:8]})")
