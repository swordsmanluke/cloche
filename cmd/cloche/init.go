package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cloche-dev/cloche/internal/config"
	"golang.org/x/term"
)

// stdinOverride is set in tests to inject fake stdin, bypassing the TTY check.
var stdinOverride io.Reader

// sshKeygenFunc is the hook used to generate SSH keys; replaced in tests.
var sshKeygenFunc = func(keyFile, comment string) error {
	cmd := exec.Command("ssh-keygen", "-t", "ed25519", "-f", keyFile, "-C", comment, "-N", "")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// gitOriginURLFunc is the hook used to get the git remote origin URL; replaced in tests.
var gitOriginURLFunc = func() (string, error) {
	out, err := exec.Command("git", "remote", "get-url", "origin").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// bdLookPathFunc is the hook used to locate the beads CLI; replaced in tests.
var bdLookPathFunc = func() (string, error) {
	return exec.LookPath("bd")
}

// bdRunFunc is the hook used to run the beads CLI; replaced in tests.
// Returns trimmed stdout.
var bdRunFunc = func(args ...string) (string, error) {
	out, err := exec.Command("bd", args...).Output()
	return strings.TrimSpace(string(out)), err
}

var workflowTemplate = `workflow %s {
  step implement {
    prompt  = file(".cloche/prompts/implement.md")
    results = [success, fail]
  }

  step commit {
    run     = "git add -A && git commit -m \"implement task changes\" || true"
    results = [success, fail]
  }

  step test {
    run     = "echo 'TODO(cloche-init): replace with your test command'"
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
`

var dockerfileTemplate = `FROM %s
USER root

# TODO(cloche-init): install your project's runtime and build dependencies.
# Uncomment and adapt one of the examples below, or write your own.
#
# Python:
#   RUN apt-get update && apt-get install -y --no-install-recommends python3 python3-pip \
#    && rm -rf /var/lib/apt/lists/*
#
# Node.js (LTS via NodeSource):
#   RUN curl -fsSL https://deb.nodesource.com/setup_20.x | bash - \
#    && apt-get install -y nodejs && rm -rf /var/lib/apt/lists/*
#
# Go (use the official golang image as base instead):
#   FROM golang:1.22 AS base
#
# Java:
#   RUN apt-get update && apt-get install -y --no-install-recommends default-jdk \
#    && rm -rf /var/lib/apt/lists/*
#
# Ruby:
#   RUN apt-get update && apt-get install -y --no-install-recommends ruby ruby-dev \
#    && rm -rf /var/lib/apt/lists/*

USER agent
`

var implementPrompt = `# Implement Task

Retrieve the task description by running:

    cat /workspace/$(clo get task_prompt_path)

The prepare-prompt host step wrote that file into the run directory (which is
mounted into this container) and stored its path in the run's key-value store;
clo get reads it back from in here. This file path is how task data crosses the
host/container boundary — see docs/init/4-passing-data-between-steps.md.

If that file is missing or empty, ABORT and report fail.

## Project Context

TODO(cloche-init): describe your project here so the agent has the context it needs. Examples:
- Language: Go — run tests with "go test ./..."
- Language: Node.js/TypeScript — run tests with "npm test"
- Language: Python — run tests with "pytest"
- Key constraints: follow existing patterns, don't modify generated files

## Guidelines
- Follow existing project conventions
- Write tests for new functionality
- Run tests locally before declaring success
`

var fixTestsPrompt = `The tests are failing. Fix the code so all tests pass.

Do not modify the test files — fix the implementation instead.

## Test Output

{{ $prev_output }}
`

var defaultConfigTOMLTemplate = `# Cloche project configuration
active = true

[daemon]
image = "%s"

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

[git]
# name    = ""     # override global git identity for this project
# email   = ""
# ssh_key = ""     # path to private key used for git push
`

var defaultClocheignore = `# Files excluded from the container workspace.
# Uses gitignore-style patterns (*, ?, **).

# Cloche runtime state
.cloche/logs/
.cloche/runs/

# Extraction worktrees live on the host.
.gitworktrees/

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
`

var versionContent = "1\n"

var hostWorkflowTemplate = `// Host orchestration workflows, run by the daemon on the host machine.
//
// The loop is two-phase: list-tasks is polled to find ready work, then main
// runs once per dispatched task with CLOCHE_TASK_ID set in every step's
// environment. See docs/init/3-run-the-loop.md.
//
// Data flows between steps through the run's key-value store (cloche get/set
// on the host, clo get/set inside containers) — see
// docs/init/4-passing-data-between-steps.md.

workflow list-tasks {
  host {}

  step get-tasks {
    run     = "python3 .cloche/scripts/get-tasks.py"
    results = [success, fail]
  }

  get-tasks:success -> done
  get-tasks:fail    -> abort
}

workflow main {
  host {}

  // Mark the task in_progress in beads.
  step claim-task {
    run     = "python3 .cloche/scripts/claim-task.py"
    results = [success, fail]
  }

  // Write the task prompt to a file and publish its path in the KV store.
  step prepare-prompt {
    run     = "python3 .cloche/scripts/prepare-prompt.py"
    results = [success, fail]
  }

  // Run the container workflow. The daemon pre-creates a git branch and
  // worktree for the results and publishes child_branch in the KV store.
  step develop {
    workflow_name = "%s"
    results       = [success, fail]
  }

  // Rebase the daemon-created branch onto the base branch and fast-forward.
  step merge {
    run     = "python3 .cloche/scripts/merge.py"
    results = [success, fail]
  }

  // Agent step: resolve rebase conflicts in the worktree, then merge re-runs.
  step fix-merge {
    prompt  = file(".cloche/prompts/fix-merge.md")
    results = [success, fail]
  }

  // Close the task in beads.
  step close-task {
    run     = "python3 .cloche/scripts/close-task.py"
    results = [success, fail]
  }

  // Remove the leftover worktree and branch.
  step cleanup {
    run     = "python3 .cloche/scripts/cleanup.py"
    results = [success, fail]
  }

  // Emergency brake: reopen the task and stop the loop for human review.
  step unclaim {
    run     = "python3 .cloche/scripts/unclaim.py"
    results = [success, fail]
  }

  claim-task:success     -> prepare-prompt
  claim-task:fail        -> abort
  prepare-prompt:success -> develop
  prepare-prompt:fail    -> unclaim
  develop:success        -> merge
  develop:fail           -> unclaim
  merge:success          -> close-task
  merge:fail             -> fix-merge
  fix-merge:success      -> merge
  fix-merge:fail         -> unclaim
  close-task:success     -> cleanup
  close-task:fail        -> unclaim
  cleanup:success        -> done
  cleanup:fail           -> unclaim
  unclaim:success        -> abort
  unclaim:fail           -> abort
}
`

var getTasksPyScript = `#!/usr/bin/env python3
"""Print ready tasks from beads (bd) as JSONL for the cloche loop.

This is the task-tracker integration point. The loop runs this script and
expects zero or more JSON objects on stdout, one task per line:

    {"id": "...", "title": "...", "description": "...", "status": "open"}

Only tasks with status "open" are dispatched. To use a different tracker
(GitHub Issues, Jira, Linear, ...), replace this script with one that emits
the same JSONL — see docs/init/6-swapping-the-task-tracker.md.

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
    print("task tracker — see docs/init/6-swapping-the-task-tracker.md.", file=sys.stderr)
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
`

var claimTaskPyScript = `#!/usr/bin/env python3
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
`

var preparePromptPyScript = `#!/usr/bin/env python3
"""Build the agent's task prompt and pass it to the container via the KV store.

This is the host-to-container data handoff, the pattern to copy whenever a
step needs to send data to another step (docs/init/4-passing-data-between-steps.md):

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
`

var mergePyScript = `#!/usr/bin/env python3
"""Rebase the daemon-created result branch onto the base branch and fast-forward.

Before the develop sub-workflow ran, the daemon already created a branch and a
worktree for its results (.gitworktrees/cloche/<suffix>), extracted the
container's changes into it, and published the branch name in the KV store as
child_branch. This script only consumes that worktree — it never creates one.
See docs/init/5-how-changes-land.md.

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
`

var closeTaskPyScript = `#!/usr/bin/env python3
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
`

var cleanupPyScript = `#!/usr/bin/env python3
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
`

var unclaimPyScript = `#!/usr/bin/env python3
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
`

var fixMergePrompt = `# Fix Merge Conflicts

The merge step failed: rebasing the agent's result branch onto the base branch
produced conflicts. Everything you need is filled in below from the run's
key-value store via prompt template variables
(docs/init/4-passing-data-between-steps.md):

- Worktree with the conflicting branch checked out: {{ $worktree_path }}
- Base branch to rebase onto: {{ $base_branch }}

The previous rebase attempt was aborted, so start it again:

1. Run:

   git -C {{ $worktree_path }} rebase {{ $base_branch }}

2. Resolve each conflicted file semantically — understand both sides and
   integrate them; do not just pick one. After editing each file, run
   git -C {{ $worktree_path }} add <file>.

3. Complete the rebase:

   git -C {{ $worktree_path }} rebase --continue

Do not run rebase --abort, do not merge, and do not delete any branch — after
you report success the merge step re-runs and performs the fast-forward itself.

Report success when the rebase completes cleanly; report fail if the conflicts
cannot be resolved.
`

var testClocheScript = `#!/usr/bin/env python3
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
`

// sanitizeProjectBasename replaces non-alphanumeric characters with underscores.
func sanitizeProjectBasename(name string) string {
	var b strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// parseGitHubOwnerRepo extracts the owner and repo from a GitHub remote URL.
// Supports both HTTPS (https://github.com/owner/repo[.git]) and
// SSH (git@github.com:owner/repo[.git]) formats.
func parseGitHubOwnerRepo(remoteURL string) (owner, repo string) {
	var path string
	if after, ok := strings.CutPrefix(remoteURL, "https://github.com/"); ok {
		path = strings.TrimSuffix(after, ".git")
	} else if after, ok := strings.CutPrefix(remoteURL, "git@github.com:"); ok {
		path = strings.TrimSuffix(after, ".git")
	} else {
		return "", ""
	}
	owner, repo, found := strings.Cut(path, "/")
	if !found || owner == "" || repo == "" {
		return "", ""
	}
	return owner, repo
}

// detectGitHubDeployKeyURL returns the GitHub deploy-keys settings URL if the
// current project's origin remote is a GitHub repository.
func detectGitHubDeployKeyURL() (string, bool) {
	remoteURL, err := gitOriginURLFunc()
	if err != nil {
		return "", false
	}
	owner, repo := parseGitHubOwnerRepo(remoteURL)
	if owner == "" || repo == "" {
		return "", false
	}
	return fmt.Sprintf("https://github.com/%s/%s/settings/keys", owner, repo), true
}

// isTerminal reports whether f is connected to a real terminal.
func isTerminal(f *os.File) bool {
	return term.IsTerminal(int(f.Fd()))
}

func getStdin() io.Reader {
	if stdinOverride != nil {
		return stdinOverride
	}
	return os.Stdin
}

// writeSSHKeyToConfig writes ssh_key = "<keyPath>" into the [git] section of
// the given config.toml, replacing the commented placeholder if present.
func writeSSHKeyToConfig(configPath, keyPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not read %s: %v\n", configPath, err)
		return
	}
	lines := strings.Split(string(data), "\n")
	replaced := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ssh_key =") || strings.HasPrefix(trimmed, "ssh_key =") {
			lines[i] = fmt.Sprintf(`ssh_key = "%s"`, keyPath)
			replaced = true
			break
		}
	}
	if !replaced {
		insertIdx := -1
		for i, line := range lines {
			if strings.TrimSpace(line) == "[git]" {
				insertIdx = i + 1
				break
			}
		}
		newLine := fmt.Sprintf(`ssh_key = "%s"`, keyPath)
		if insertIdx >= 0 {
			newLines := make([]string, 0, len(lines)+1)
			newLines = append(newLines, lines[:insertIdx]...)
			newLines = append(newLines, newLine)
			newLines = append(newLines, lines[insertIdx:]...)
			lines = newLines
		} else {
			lines = append(lines, "", "[git]", newLine)
		}
	}
	if err := os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update %s: %v\n", configPath, err)
	} else {
		fmt.Fprintf(os.Stderr, "  update %s (ssh_key)\n", configPath)
	}
}

// runSSHKeySetup handles the --ssh-key flag and interactive SSH key prompt.
func runSSHKeySetup(configPath, sshKeyFlag string, nonInteractive bool, projectName string) {
	if sshKeyFlag != "" {
		if _, err := os.Stat(sshKeyFlag); err != nil {
			fmt.Fprintf(os.Stderr, "warning: ssh key %q not found; skipping config update\n", sshKeyFlag)
			return
		}
		writeSSHKeyToConfig(configPath, sshKeyFlag)
		return
	}
	if nonInteractive {
		return
	}
	if stdinOverride == nil && !isTerminal(os.Stdin) {
		return
	}
	reader := bufio.NewReader(getStdin())
	fmt.Fprintf(os.Stderr, "\nConfigure a project-specific git push key for this project? [y/N] ")
	line, _ := reader.ReadString('\n')
	if strings.ToLower(strings.TrimSpace(line)) != "y" {
		return
	}
	fmt.Fprintf(os.Stderr, "Use an existing key, generate a new one, or skip? [existing/generate/skip] ")
	line, _ = reader.ReadString('\n')
	choice := strings.ToLower(strings.TrimSpace(line))
	switch choice {
	case "existing":
		fmt.Fprintf(os.Stderr, "Path to existing SSH private key: ")
		line, _ = reader.ReadString('\n')
		keyPath := strings.TrimSpace(line)
		if _, err := os.Stat(keyPath); err != nil {
			fmt.Fprintf(os.Stderr, "warning: %q not found; skipping config update\n", keyPath)
			return
		}
		writeSSHKeyToConfig(configPath, keyPath)
	case "generate":
		home := os.Getenv("HOME")
		if home == "" {
			fmt.Fprintf(os.Stderr, "warning: $HOME not set; cannot generate SSH key\n")
			return
		}
		sanitized := sanitizeProjectBasename(projectName)
		keyFile := filepath.Join(home, ".ssh", "cloche_"+sanitized)
		comment := "cloche-bot@" + sanitized
		if err := sshKeygenFunc(keyFile, comment); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not generate SSH key: %v\n", err)
			return
		}
		os.Chmod(keyFile, 0600)
		os.Chmod(keyFile+".pub", 0644)
		writeSSHKeyToConfig(configPath, keyFile)
		if pubKey, err := os.ReadFile(keyFile + ".pub"); err == nil {
			fmt.Fprintf(os.Stderr, "\nPublic key to add to GitHub:\n\n%s\n", string(pubKey))
		}
		if deployURL, ok := detectGitHubDeployKeyURL(); ok {
			fmt.Fprintf(os.Stderr, "Add as a deploy key at: %s\n", deployURL)
		} else {
			fmt.Fprintf(os.Stderr, "Add as a user key at: https://github.com/settings/ssh/new\n")
		}
	}
}

func projectImageName() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "cloche-agent:latest"
	}
	return strings.ToLower(filepath.Base(cwd)) + "-cloche-agent:latest"
}

// ensureConfigTOML creates .cloche/config.toml with active = true if it does not exist,
// or updates active = false → active = true in an existing file.
func ensureConfigTOML(path, imageName string) {
	if _, err := os.Stat(path); err == nil {
		// File exists — update active = true if needed.
		data, err := os.ReadFile(path)
		if err == nil {
			content := string(data)
			updated := false
			if strings.Contains(content, "active = false") {
				content = strings.ReplaceAll(content, "active = false", "active = true")
				updated = true
			} else if !strings.Contains(content, "active = true") && !strings.Contains(content, "active=true") {
				// No active key found — prepend it.
				content = "active = true\n" + content
				updated = true
			}
			if updated {
				if err := os.WriteFile(path, []byte(content), 0644); err != nil {
					fmt.Fprintf(os.Stderr, "warning: could not update %s: %v\n", path, err)
				} else {
					fmt.Fprintf(os.Stderr, "  update %s\n", path)
				}
			}
		}
		return
	}
	// File doesn't exist — create it with the full template.
	content := fmt.Sprintf(defaultConfigTOMLTemplate, imageName)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "error writing %s: %v\n", path, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "  create %s\n", path)
}

// runNewProjectInit scaffolds workflow files, Dockerfile, prompts, scripts,
// and other first-time project files. Individual files that already exist are
// skipped with a warning rather than overwritten.
func runNewProjectInit(clocheDir, workflow, baseImage string, noLLM bool, agentCommand string) {
	workflowFile := filepath.Join(clocheDir, workflow+".cloche")

	// Create subdirectories needed by --new.
	for _, dir := range []string{
		filepath.Join(clocheDir, "prompts"),
		filepath.Join(clocheDir, "overrides"),
		filepath.Join(clocheDir, "scripts"),
		filepath.Join("cloche_init_test", "cloche"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating %s/: %v\n", dir, err)
			os.Exit(1)
		}
	}

	// Write all files, skipping any that already exist.
	files := []struct {
		path    string
		content string
		mode    os.FileMode
	}{
		{workflowFile, fmt.Sprintf(workflowTemplate, workflow), 0644},
		{filepath.Join(clocheDir, "Dockerfile"), fmt.Sprintf(dockerfileTemplate, baseImage), 0644},
		{filepath.Join(clocheDir, "prompts", "implement.md"), implementPrompt, 0644},
		{filepath.Join(clocheDir, "prompts", "fix-tests.md"), fixTestsPrompt, 0644},
		{filepath.Join(clocheDir, "prompts", "fix-merge.md"), fixMergePrompt, 0644},
		{filepath.Join(clocheDir, "version"), versionContent, 0644},
		{filepath.Join(clocheDir, "host.cloche"), fmt.Sprintf(hostWorkflowTemplate, workflow), 0644},
		{filepath.Join(clocheDir, "scripts", "get-tasks.py"), getTasksPyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "claim-task.py"), claimTaskPyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "prepare-prompt.py"), preparePromptPyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "merge.py"), mergePyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "close-task.py"), closeTaskPyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "cleanup.py"), cleanupPyScript, 0755},
		{filepath.Join(clocheDir, "scripts", "unclaim.py"), unclaimPyScript, 0755},
		{".clocheignore", defaultClocheignore, 0644},
		{filepath.Join("cloche_init_test", "cloche", "test_cloche.py"), testClocheScript, 0644},
	}

	for _, f := range files {
		if _, err := os.Stat(f.path); err == nil {
			fmt.Fprintf(os.Stderr, "  skip %s (already exists)\n", f.path)
			continue
		}
		if err := os.WriteFile(f.path, []byte(f.content), f.mode); err != nil {
			fmt.Fprintf(os.Stderr, "error writing %s: %v\n", f.path, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "  create %s\n", f.path)
	}

	// Bootstrap beads (bd) task tracking with the starter validation tasks.
	runBeadsBootstrap()

	// LLM-assisted init: fill in TODO(cloche-init) placeholders.
	if !noLLM {
		runLLMInitPhase(agentCommand, workflow)
	}
}

// starterTasks are the validation tasks created in beads during --new
// bootstrap. The second depends on the first, exercising the dependency
// gate in get-tasks.py.
var starterTasks = []struct{ title, desc string }{
	{
		"Validate Agent works",
		"Create a file, ./agent_test containing the string 'I exist!'",
	},
	{
		"Clean up cloche test files",
		"Delete ./agent_test and the cloche_init_test/ directory — they were created to validate the Cloche setup and are no longer needed",
	},
}

// runScaffoldCommit commits the generated scaffold so it is visible to
// containers, which are seeded from a clean git snapshot of the last commit.
// Skipped (with guidance) when not in a git repo, when the user already has
// staged changes, or when there is nothing to commit. Failures are warnings.
func runScaffoldCommit() {
	git := func(args ...string) (string, error) {
		out, err := exec.Command("git", args...).CombinedOutput()
		return strings.TrimSpace(string(out)), err
	}

	if _, err := git("rev-parse", "--git-dir"); err != nil {
		fmt.Fprintf(os.Stderr, "\nwarning: not a git repository. Cloche requires git: containers are seeded\n"+
			"from a clean git snapshot. Run 'git init && git add -A && git commit' before 'cloche loop'.\n")
		return
	}

	// Don't fold the user's own staged work into the scaffold commit.
	if _, err := git("diff", "--cached", "--quiet"); err != nil {
		fmt.Fprintf(os.Stderr, "\nwarning: you have staged changes, so the scaffold was not auto-committed.\n"+
			"Commit the scaffold yourself before 'cloche loop' (containers see only committed files).\n")
		return
	}

	for _, path := range []string{".cloche", ".clocheignore", ".gitignore", "cloche_init_test", ".beads", "AGENTS.md"} {
		if _, err := os.Stat(path); err == nil {
			git("add", path)
		}
	}

	if _, err := git("diff", "--cached", "--quiet"); err == nil {
		return // nothing new to commit
	}

	if out, err := git("commit", "-m", "Add cloche scaffold"); err != nil {
		git("reset") // unstage so the user is back where they started
		fmt.Fprintf(os.Stderr, "\nwarning: could not commit the scaffold: %v\n%s\n"+
			"Commit it yourself before 'cloche loop' (containers see only committed files).\n", err, out)
		return
	}
	fmt.Fprintf(os.Stderr, "  commit scaffold (containers are seeded from the last git commit)\n")
}

// runBeadsBootstrap initializes beads (bd init) and creates the starter
// validation tasks. A missing bd CLI or bd failures produce warnings, never
// abort the init.
func runBeadsBootstrap() {
	if _, err := bdLookPathFunc(); err != nil {
		fmt.Fprintf(os.Stderr, `
warning: the beads CLI (bd) was not found on PATH.
The generated task workflow uses beads for task tracking. Install it
(https://github.com/steveyegge/beads), then re-run 'cloche init --new' to
create the starter tasks — or swap in your own task tracker, see
docs/init/6-swapping-the-task-tracker.md. 'cloche doctor' also checks for bd.
`)
		return
	}

	// A .beads/ directory means beads is already set up here — leave it
	// alone and don't re-create the starter tasks on re-init.
	if _, err := os.Stat(".beads"); err == nil {
		fmt.Fprintf(os.Stderr, "  skip bd init (.beads/ already exists)\n")
		return
	}

	if out, err := bdRunFunc("init", "-q"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: bd init failed: %v\n%s\n", err, out)
		return
	}
	fmt.Fprintf(os.Stderr, "  create .beads/ (bd init)\n")

	firstID := ""
	for i, task := range starterTasks {
		args := []string{"create", task.title, "-d", task.desc, "--silent"}
		if i > 0 && firstID != "" {
			args = append(args, "--deps", firstID)
		}
		id, err := bdRunFunc(args...)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: bd create %q failed: %v\n", task.title, err)
			continue
		}
		if i == 0 {
			firstID = id
		}
		fmt.Fprintf(os.Stderr, "  create bead %s (%s)\n", id, task.title)
	}
}

func cmdInit(args []string) {
	workflow := "develop"
	baseImage := "cloche-agent:latest"
	noLLM := false
	agentCommand := ""
	newProject := false
	installShellHelpers := false
	nonInteractive := false
	noCommit := false
	sshKey := ""

	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--workflow":
			if i+1 < len(args) {
				i++
				workflow = args[i]
			}
		case "--base-image":
			if i+1 < len(args) {
				i++
				baseImage = args[i]
			}
		case "--no-llm":
			noLLM = true
		case "--agent-command":
			if i+1 < len(args) {
				i++
				agentCommand = args[i]
			}
		case "--new", "-n":
			newProject = true
		case "--install-shell-helpers":
			installShellHelpers = true
		case "--non-interactive":
			nonInteractive = true
		case "--no-commit":
			noCommit = true
		case "--ssh-key":
			if i+1 < len(args) {
				i++
				sshKey = args[i]
			}
		}
	}

	imageName := projectImageName()
	clocheDir := ".cloche"

	// === Core behavior (always) ===

	// 1. Create .cloche/ directory and core subdirectories.
	for _, dir := range []string{
		clocheDir,
		filepath.Join(clocheDir, "prompts"),
		filepath.Join(clocheDir, "overrides"),
		filepath.Join(clocheDir, "scripts"),
	} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			fmt.Fprintf(os.Stderr, "error creating %s/: %v\n", dir, err)
			os.Exit(1)
		}
	}

	// 2. Create or update config.toml with active = true.
	ensureConfigTOML(filepath.Join(clocheDir, "config.toml"), imageName)

	// 3. Add .gitignore entries for runtime state.
	addGitignoreEntries([]string{
		".cloche/logs/",
		".cloche/runs/",
		".cloche/output/",
		".cloche/history.log",
		".cloche/.loop-stopped",
		".gitworktrees/",
	})

	// 4. Create global daemon config if it doesn't exist.
	if cfgPath, err := config.WriteGlobalConfigIfAbsent(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not write global config: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "  create %s\n", cfgPath)
	}

	// === --new / -n flag ===
	if newProject {
		runNewProjectInit(clocheDir, workflow, baseImage, noLLM, agentCommand)
	}

	// === --install-shell-helpers flag ===
	if installShellHelpers {
		home := os.Getenv("HOME")
		if home != "" {
			completionsDir := filepath.Join(home, ".cloche", "completions")
			generateCompletionScripts(completionsDir)
		}
	}

	// === SSH key setup (--ssh-key flag or interactive prompt) ===
	cwd, _ := os.Getwd()
	runSSHKeySetup(filepath.Join(clocheDir, "config.toml"), sshKey, nonInteractive, filepath.Base(cwd))

	// === Commit the scaffold (after all file writes, including SSH config) ===
	if newProject && !noCommit {
		runScaffoldCommit()
	}

	if newProject {
		workflowFile := filepath.Join(clocheDir, workflow+".cloche")
		fmt.Fprintf(os.Stderr, "\nInitialized Cloche project in %s\n", filepath.Base(cwd))
		fmt.Fprintf(os.Stderr, "\nNext steps:\n")
		fmt.Fprintf(os.Stderr, "  1. Read docs/init/ in the cloche repo — six short setup tutorials\n")
		fmt.Fprintf(os.Stderr, "  2. Edit .cloche/config.toml           — review settings\n")
		fmt.Fprintf(os.Stderr, "  3. Edit %s        — adjust the test command for your project\n", workflowFile)
		fmt.Fprintf(os.Stderr, "  4. Edit .cloche/Dockerfile            — add your project's dependencies\n")
		fmt.Fprintf(os.Stderr, "  5. git commit any edits               — containers only see committed files\n")
		fmt.Fprintf(os.Stderr, "  6. cloche loop                        — start the orchestration loop\n")
		fmt.Fprintf(os.Stderr, "\nThe project image (%s) builds automatically on first run;\n", imageName)
		fmt.Fprintf(os.Stderr, "pre-build it with: docker build -t %s -f .cloche/Dockerfile .\n", imageName)
		fmt.Fprintf(os.Stderr, "\nRun 'bd ready' to see the starter tasks: task #1 has the agent create a\n")
		fmt.Fprintf(os.Stderr, "file to verify the setup end-to-end; task #2 unblocks afterwards and cleans up.\n")
	} else {
		fmt.Fprintf(os.Stderr, "\nProject registered at %s\n", filepath.Base(cwd))
		if !installShellHelpers {
			fmt.Fprintf(os.Stderr, "Run 'cloche init --new' to generate workflow files and Dockerfile.\n")
		}
	}
}

func removeGitignoreEntries(entries []string) {
	const gitignore = ".gitignore"

	existing, err := os.ReadFile(gitignore)
	if err != nil {
		return
	}

	lines := strings.Split(string(existing), "\n")
	removeSet := make(map[string]bool, len(entries))
	for _, e := range entries {
		removeSet[e] = true
	}

	var filtered []string
	for _, line := range lines {
		if !removeSet[strings.TrimSpace(line)] {
			filtered = append(filtered, line)
		}
	}

	result := strings.Join(filtered, "\n")
	os.WriteFile(gitignore, []byte(result), 0644)
}

func addGitignoreEntries(entries []string) {
	const gitignore = ".gitignore"

	existing, _ := os.ReadFile(gitignore)
	content := string(existing)

	var toAdd []string
	for _, entry := range entries {
		if !strings.Contains(content, entry) {
			toAdd = append(toAdd, entry)
		}
	}
	if len(toAdd) == 0 {
		return
	}

	if len(content) > 0 && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += strings.Join(toAdd, "\n") + "\n"

	if err := os.WriteFile(gitignore, []byte(content), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not update .gitignore: %v\n", err)
	}
}
