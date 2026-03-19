# `cloche init` v2 Default Scaffold Design

**Date:** 2026-03-19
**Status:** Design

## Problem

Setting up a new cloche project is a pain point. The current scaffold produces
placeholder workflows that don't actually exercise the system end-to-end. A new
user has no way to verify their environment works without writing real workflows
first. The demo scripts are Bash (less portable/readable), the Dockerfile
references a generic base image, and the host workflow is a single-phase
`main` that doesn't demonstrate the three-phase orchestration loop.

## Solution

Replace the scaffold with a fully functional three-phase orchestration setup
backed by a JSONL task file. The first task ("Validate Agent works") asks the
agent to create a file that a unittest validates — giving the user immediate
feedback that containers, agents, and workflows are all wired correctly. The
second task cleans up after itself. All scripts are Python. The Dockerfile is
based on `cloche-agent` with Python3 pre-installed.

## Design Details

### File Layout

`cloche init --workflow develop` (default) produces:

```
.cloche/
├── config.toml
├── Dockerfile
├── version
├── develop.cloche              # Container workflow
├── host.cloche                 # Host workflows (list-tasks, main, finalize)
├── prompts/
│   ├── implement.md
│   ├── fix-tests.md
│   └── fix-merge.md
├── scripts/
│   ├── get-tasks.py
│   ├── claim-task.py
│   ├── prepare-merge.py
│   ├── merge.py
│   ├── release-task.py
│   ├── cleanup.py
│   └── unclaim.py
├── overrides/
task_list.json
test/cloche/test_cloche.py
.clocheignore
```

Files at project root: `task_list.json`, `test/cloche/test_cloche.py`,
`.clocheignore`. Everything else under `.cloche/`.

### Dockerfile

Based on `cloche-agent` with Python3 installed:

```dockerfile
FROM cloche-agent:latest
USER root

RUN apt-get update \
 && apt-get install -y --no-install-recommends python3 \
 && rm -rf /var/lib/apt/lists/*

USER agent
```

The `--base-image` flag still overrides the FROM line.

### task_list.json

JSONL format at the project root. Two seed tasks:

```jsonl
{"id": "1", "title": "Validate Agent works", "description": "Create a file, ./agent_test containing the string 'I exist!'", "status": "open"}
{"id": "2", "title": "Clean up cloche test files", "description": "Delete ./agent_test and test/cloche/test_cloche.py - they were created for validation and we're done now", "status": "open"}
```

### test/cloche/test_cloche.py

A unittest file that validates the agent's work. Created by `init` but **not**
`./agent_test` — that's the agent's job.

```python
#!/usr/bin/env python3
"""Cloche environment validation tests.

These tests verify that the agent successfully completed the setup task.
Delete this file once validation is complete (task #2 does this automatically).
"""
import os
import unittest

PROJECT_ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

class TestAgentSetup(unittest.TestCase):
    def test_agent_test_file_exists(self):
        path = os.path.join(PROJECT_ROOT, "agent_test")
        self.assertTrue(os.path.isfile(path), "agent_test file does not exist")

    def test_agent_test_file_contents(self):
        path = os.path.join(PROJECT_ROOT, "agent_test")
        with open(path) as f:
            contents = f.read().strip()
        self.assertEqual(contents, "I exist!")

if __name__ == "__main__":
    unittest.main()
```

### Container Workflow: develop.cloche

Four steps: implement → commit → test → fix-tests (retry loop).

```
workflow "develop" {
  step implement {
    prompt  = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step commit {
    run     = "git add -A && git commit -m \"implement task changes\" || true"
    results = [success, fail]
  }

  step test {
    run     = "python3 -m unittest discover -s test -v 2>&1"
    results = [success, fail]
  }

  step fix-tests {
    prompt       = file(".cloche/prompts/fix-tests.md")
    max_attempts = 2
    results      = [success, fail]
  }

  implement:success  -> commit
  implement:fail     -> abort
  commit:success     -> test
  commit:fail        -> abort
  test:success       -> done
  test:fail          -> fix-tests
  fix-tests:success  -> test
  fix-tests:fail     -> abort
}
```

### Host Workflow: host.cloche

Four workflows in one file: `list-tasks`, `main`, `finalize`.

#### list-tasks

```
workflow "list-tasks" {
  host {}

  step get-tasks {
    run     = "python3 .cloche/scripts/get-tasks.py"
    results = [success, fail]
  }

  get-tasks:success -> done
  get-tasks:fail    -> abort
}
```

#### main

```
workflow "main" {
  host {}

  step claim-task {
    run     = "python3 .cloche/scripts/claim-task.py"
    results = [success, fail]
  }

  step develop {
    workflow_name = "develop"
    results       = [success, fail]
  }

  claim-task:success -> develop
  claim-task:fail    -> abort
  develop:success    -> done
  develop:fail       -> done
}
```

The `main` workflow is kept simple — claim the task, run the container workflow.
Merge and cleanup happen in `finalize` so they run regardless of success/failure.

#### finalize

```
workflow "finalize" {
  host {}

  step prepare-merge {
    run     = "python3 .cloche/scripts/prepare-merge.py"
    results = [success, fail]
  }

  step fix-merge {
    prompt  = file(".cloche/prompts/fix-merge.md")
    results = [success, fail]
  }

  step merge {
    run     = "python3 .cloche/scripts/merge.py"
    results = [success, fail]
  }

  step release-task {
    run     = "python3 .cloche/scripts/release-task.py"
    results = [success, fail]
  }

  step cleanup {
    run     = "python3 .cloche/scripts/cleanup.py"
    results = [success, fail]
  }

  step unclaim {
    run     = "python3 .cloche/scripts/unclaim.py"
    results = [success, fail]
  }

  prepare-merge:success -> merge
  prepare-merge:fail    -> fix-merge
  fix-merge:success     -> merge
  fix-merge:fail        -> unclaim
  merge:success         -> release-task
  merge:fail            -> fix-merge
  release-task:success  -> cleanup
  release-task:fail     -> unclaim
  cleanup:success       -> done
  cleanup:fail          -> unclaim
  unclaim:success       -> abort
  unclaim:fail          -> abort
}
```

### Prompt Templates

#### .cloche/prompts/implement.md

```markdown
Implement the following change in this project.

## Task

{task_description}

## Guidelines
- Follow existing project conventions
- Write tests for new functionality
- Run tests locally before declaring success
```

(The `{task_description}` placeholder is filled by the adapter from the task
body / previous step output.)

#### .cloche/prompts/fix-tests.md

```markdown
The tests are failing. Fix the code so all tests pass.

Do not modify the test files — fix the implementation instead.

## Test Output

{previous_output}
```

#### .cloche/prompts/fix-merge.md

```markdown
A rebase of the agent's branch onto the base branch has failed due to conflicts.

The conflicting worktree is at the path stored in the `worktree_path` context key
(retrieve with `cloche get worktree_path`).

Resolve the conflicts in the worktree, then run:
  git -C <worktree_path> rebase --continue

Do not abort the rebase. If you cannot resolve the conflicts, report failure.
```

### Scripts

All scripts are Python 3, executable (0755), and use only the standard library.
They read environment variables set by the host executor and use `cloche get/set`
for inter-step state.

#### .cloche/scripts/get-tasks.py

Reads `task_list.json`, emits the first open task as JSONL to stdout and
`$CLOCHE_STEP_OUTPUT`. Contains comments explaining how to replace this with a
real task tracker integration.

```python
#!/usr/bin/env python3
"""Read the next open task from task_list.json.

Replace this script with one that reads from your task tracker of choice
(Linear, Jira, GitHub Issues, etc.) and returns ready tasks as JSONL:

    {"id": "...", "title": "...", "description": "...", "status": "open"}

One JSON object per line. The orchestration loop picks the first open task.
"""
import json
import os
import sys

task_file = os.path.join(os.environ.get("CLOCHE_PROJECT_DIR", "."), "task_list.json")

with open(task_file) as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        task = json.loads(line)
        if task.get("status") == "open":
            output = json.dumps(task)
            print(output)
            output_path = os.environ.get("CLOCHE_STEP_OUTPUT")
            if output_path:
                with open(output_path, "w") as out:
                    out.write(output + "\n")
            sys.exit(0)

# No open tasks — exit success with empty output (loop idles)
output_path = os.environ.get("CLOCHE_STEP_OUTPUT")
if output_path:
    with open(output_path, "w") as out:
        out.write("")
```

#### .cloche/scripts/claim-task.py

Sets the assigned task's status to `"in-progress"` in `task_list.json`.

```python
#!/usr/bin/env python3
"""Mark the assigned task as in-progress in task_list.json."""
import json
import os
import sys

task_id = os.environ.get("CLOCHE_TASK_ID", "")
project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")
task_file = os.path.join(project_dir, "task_list.json")

if not task_id:
    print("error: CLOCHE_TASK_ID not set", file=sys.stderr)
    sys.exit(1)

tasks = []
with open(task_file) as f:
    for line in f:
        line = line.strip()
        if line:
            tasks.append(json.loads(line))

found = False
for task in tasks:
    if task["id"] == task_id:
        task["status"] = "in-progress"
        found = True
        break

if not found:
    print(f"error: task {task_id} not found", file=sys.stderr)
    sys.exit(1)

with open(task_file, "w") as f:
    for task in tasks:
        f.write(json.dumps(task) + "\n")

print(f"Claimed task {task_id}")
```

#### .cloche/scripts/prepare-merge.py

Creates a worktree for the agent's branch and rebases onto the base branch.
Stores the worktree path via `cloche set` for downstream steps.

```python
#!/usr/bin/env python3
"""Create a worktree and rebase the agent's branch onto the base branch."""
import os
import subprocess
import sys

run_id = os.environ.get("CLOCHE_MAIN_RUN_ID", "")
project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")

if not run_id:
    print("error: CLOCHE_MAIN_RUN_ID not set", file=sys.stderr)
    sys.exit(1)

branch = f"cloche/{run_id}"
worktree_dir = os.path.join(project_dir, ".gitworktrees", "merge", run_id)

# Verify branch exists
try:
    subprocess.run(
        ["git", "-C", project_dir, "rev-parse", "--verify", branch],
        check=True, capture_output=True,
    )
except subprocess.CalledProcessError:
    print(f"error: branch {branch} does not exist", file=sys.stderr)
    sys.exit(1)

base_branch = subprocess.run(
    ["git", "-C", project_dir, "rev-parse", "--abbrev-ref", "HEAD"],
    check=True, capture_output=True, text=True,
).stdout.strip()

os.makedirs(os.path.dirname(worktree_dir), exist_ok=True)

env = {**os.environ,
       "GIT_AUTHOR_NAME": "cloche", "GIT_AUTHOR_EMAIL": "cloche@local",
       "GIT_COMMITTER_NAME": "cloche", "GIT_COMMITTER_EMAIL": "cloche@local"}

subprocess.run(
    ["git", "-C", project_dir, "worktree", "add", worktree_dir, branch],
    check=True, env=env,
)

# Store worktree path for fix-merge / merge steps
subprocess.run(["cloche", "set", "worktree_path", worktree_dir], check=True)
subprocess.run(["cloche", "set", "base_branch", base_branch], check=True)

# Rebase onto base branch
result = subprocess.run(
    ["git", "-C", worktree_dir, "rebase", base_branch],
    env=env,
)
if result.returncode != 0:
    subprocess.run(["git", "-C", worktree_dir, "rebase", "--abort"], capture_output=True)
    print(f"error: rebase failed — worktree preserved at {worktree_dir}", file=sys.stderr)
    sys.exit(1)

print(f"Rebased {branch} onto {base_branch}")
```

#### .cloche/scripts/merge.py

Fast-forwards the base branch to the rebased head, cleans up the worktree.

```python
#!/usr/bin/env python3
"""Fast-forward base branch to the rebased agent branch."""
import subprocess
import sys

worktree_dir = subprocess.run(
    ["cloche", "get", "worktree_path"], check=True, capture_output=True, text=True,
).stdout.strip()

project_dir = subprocess.run(
    ["cloche", "get", "base_branch"], check=True, capture_output=True, text=True,
)  # just to confirm context is available

run_id = __import__("os").environ.get("CLOCHE_MAIN_RUN_ID", "")
project_dir_env = __import__("os").environ.get("CLOCHE_PROJECT_DIR", ".")
branch = f"cloche/{run_id}"

env = {**__import__("os").environ,
       "GIT_AUTHOR_NAME": "cloche", "GIT_AUTHOR_EMAIL": "cloche@local",
       "GIT_COMMITTER_NAME": "cloche", "GIT_COMMITTER_EMAIL": "cloche@local"}

# Get rebased HEAD
rebased_head = subprocess.run(
    ["git", "-C", worktree_dir, "rev-parse", "HEAD"],
    check=True, capture_output=True, text=True,
).stdout.strip()

# Remove worktree before merging
subprocess.run(
    ["git", "-C", project_dir_env, "worktree", "remove", "--force", worktree_dir],
    capture_output=True,
)

# Update branch ref and fast-forward
subprocess.run(
    ["git", "-C", project_dir_env, "update-ref", f"refs/heads/{branch}", rebased_head],
    check=True, env=env,
)
subprocess.run(
    ["git", "-C", project_dir_env, "merge", "--ff-only", branch],
    check=True, env=env,
)

# Delete the feature branch
subprocess.run(
    ["git", "-C", project_dir_env, "branch", "-D", branch],
    capture_output=True,
)

print(f"Merged {branch} ({rebased_head[:8]})")
```

#### .cloche/scripts/release-task.py

Marks the task as `"done"` in `task_list.json` and moves it to the end.

```python
#!/usr/bin/env python3
"""Mark the completed task as done and move it to the end of task_list.json."""
import json
import os
import sys

task_id = os.environ.get("CLOCHE_TASK_ID", "")
project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")
task_file = os.path.join(project_dir, "task_list.json")

if not task_id:
    print("error: CLOCHE_TASK_ID not set", file=sys.stderr)
    sys.exit(1)

tasks = []
with open(task_file) as f:
    for line in f:
        line = line.strip()
        if line:
            tasks.append(json.loads(line))

target = None
remaining = []
for task in tasks:
    if task["id"] == task_id:
        task["status"] = "done"
        target = task
    else:
        remaining.append(task)

if target is None:
    print(f"error: task {task_id} not found", file=sys.stderr)
    sys.exit(1)

remaining.append(target)

with open(task_file, "w") as f:
    for task in remaining:
        f.write(json.dumps(task) + "\n")

print(f"Released task {task_id}")
```

#### .cloche/scripts/cleanup.py

Removes the worktree and branch from the develop run.

```python
#!/usr/bin/env python3
"""Clean up the worktree and branch from the develop run."""
import os
import subprocess

run_id = os.environ.get("CLOCHE_MAIN_RUN_ID", "")
project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")

if not run_id:
    print("warning: CLOCHE_MAIN_RUN_ID not set, skipping cleanup")
else:
    branch = f"cloche/{run_id}"
    worktree_dir = os.path.join(project_dir, ".gitworktrees", "cloche", run_id)

    if os.path.isdir(worktree_dir):
        subprocess.run(
            ["git", "-C", project_dir, "worktree", "remove", "--force", worktree_dir],
            capture_output=True,
        )

    subprocess.run(
        ["git", "-C", project_dir, "worktree", "prune"],
        capture_output=True,
    )

    subprocess.run(
        ["git", "-C", project_dir, "branch", "-D", branch],
        capture_output=True,
    )

print(f"Cleaned up run {run_id or 'unknown'}")
```

#### .cloche/scripts/unclaim.py

Resets the task to `"open"` and halts the orchestration loop.

```python
#!/usr/bin/env python3
"""Reset the task to open and stop the orchestration loop.

This is the emergency brake — it halts all automated work so a human
can investigate what went wrong.
"""
import json
import os
import subprocess
import sys

task_id = os.environ.get("CLOCHE_TASK_ID", "")
project_dir = os.environ.get("CLOCHE_PROJECT_DIR", ".")
task_file = os.path.join(project_dir, "task_list.json")

if task_id:
    tasks = []
    with open(task_file) as f:
        for line in f:
            line = line.strip()
            if line:
                tasks.append(json.loads(line))

    for task in tasks:
        if task["id"] == task_id:
            task["status"] = "open"
            break

    with open(task_file, "w") as f:
        for task in tasks:
            f.write(json.dumps(task) + "\n")

    print(f"Unclaimed task {task_id}")

# Stop the loop — human must investigate and resume
subprocess.run(["cloche", "loop", "stop"])
print("Loop stopped — investigate and run 'cloche loop resume' when ready")
```

### config.toml

```toml
# Cloche project configuration
# Set active = true so cloched picks up tasks automatically.
active = false

# [orchestration]
# concurrency              = 1
# stagger_seconds          = 1.0
# dedup_seconds            = 300.0
# stop_on_error            = false
# max_consecutive_failures = 3

[evolution]
enabled            = true
debounce_seconds   = 30
min_confidence     = "medium"
max_prompt_bullets = 50
```

### .clocheignore

```
# Files excluded from the container workspace.
# Uses gitignore-style patterns (*, ?, **).

# Cloche runtime state
.cloche/logs/
.cloche/runs/

# Common large/generated directories
node_modules/
.venv/
venv/
__pycache__/
dist/
build/
.next/
target/

# IDE / editor
.idea/
.vscode/
*.swp
*.swo
```

### .gitignore entries

Added by `addGitignoreEntries`:

```
.cloche/logs/
.cloche/runs/
.gitworktrees/
```

The `removeGitignoreEntries` call is removed — new projects have nothing to
clean up.

### Post-init guidance

```
Initialized Cloche project in <dirname>

Next steps:
  1. Edit .cloche/config.toml           — set active = true
  2. Edit .cloche/develop.cloche        — adjust the test command for your project
  3. Edit .cloche/Dockerfile            — add your project's dependencies
  4. Edit .cloche/scripts/get-tasks.py  — connect to your task tracker
  5. docker build -t cloche-agent -f .cloche/Dockerfile .
  6. cloche loop                        — start the orchestration loop

The sample tasks in task_list.json verify your setup end-to-end.
Task #1 asks the agent to create a file; task #2 cleans up after itself.
```

### Changes to init.go

`cmdInit()` updates:

- **Directory creation:** Add `test/cloche/` to the mkdir list.
- **File list:** Replace all template references. Remove `prepare-prompt.sh`,
  add the seven Python scripts, `task_list.json`, and `test/cloche/test_cloche.py`.
- **Template variables:** Replace all `var` declarations at the top of `init.go`
  with the new content shown above.
- **Gitignore logic:** Remove `removeGitignoreEntries` call. Update
  `addGitignoreEntries` to use `[".cloche/logs/", ".cloche/runs/", ".gitworktrees/"]`.
- **Post-init message:** Replace the 7-step guidance with the 6-step version above.

### Error Handling

No new error paths. The existing skip-if-exists behavior applies to all new
files. Scripts exit non-zero on failure, which the host executor translates to
the `fail` result. The `unclaim` step acts as the emergency brake — it resets
task state and halts the loop so a human can investigate.

## Alternatives Considered

**Single host workflow with merge in `main`.** Rejected — merge and cleanup
should run even when `develop` fails (to avoid leaving orphaned branches and
worktrees). The `finalize` phase runs unconditionally after `main`.

**Use `pytest` in the container workflow test step.** Rejected — would require
installing pytest in the container. `python3 -m unittest discover` works with
the standard library since the test file uses the `unittest` module.

**Create `./agent_test` during init.** Rejected — the whole point is that the
agent creates it, proving the environment works end-to-end.
